package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/stevenvegt/matrix-as-demo/storage"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func main() {
	db, err := sql.Open("sqlite3", "./rooms.db")
	if err != nil {
		log.Fatal(err)
	}

	roomRepository := storage.NewRoomRepository(db)
	if err := roomRepository.InitDB(); err != nil {
		log.Fatal(err)
	}

	err = godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	asToken := os.Getenv("AS_TOKEN")
	hsToken := os.Getenv("HS_TOKEN")
	homeserverHost := os.Getenv("HOMESERVER_HOST")
	userID := os.Getenv("USER_ID")

	matrixMessages := make(chan Message)

	opts := appservice.CreateOpts{}

	opts.HomeserverDomain = homeserverHost
	opts.HomeserverURL = "https://" + homeserverHost
	opts.HostConfig = appservice.HostConfig{
		Hostname: "localhost",
		Port:     1237,
	}

	// This connfig is used for generating the registration YAML
	opts.Registration = &appservice.Registration{
		AppToken:        asToken,
		ServerToken:     hsToken,
		URL:             opts.HostConfig.Address(),
		ID:              "ASDemoConnector",
		SenderLocalpart: "_asdemo",
		Namespaces: appservice.Namespaces{
			UserIDs: []appservice.Namespace{{
				Regex:     "@_asdemo.*",
				Exclusive: true,
			}},
			RoomIDs: appservice.NamespaceList{{
				Regex:     "!.*",
				Exclusive: true,
			}},
			RoomAliases: appservice.NamespaceList{{
				Regex:     "#.*",
				Exclusive: true,
			}},
		},
	}

	// print the config
	fmt.Println("App Service registration YAML:")
	fmt.Println("------------------")
	fmt.Println(opts.Registration.YAML())
	fmt.Println("------------------")

	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	logger := zerolog.New(os.Stdout)

	as, err := appservice.CreateFull(opts)
	if err != nil {
		log.Fatal(err)
	}

	as.Log = logger

	client := as.Client(id.UserID(userID))
	resp, err := client.Whoami(context.Background())

	mh := NewMessageHandler(matrixMessages, client, roomRepository)

	ep := appservice.NewEventProcessor(as)
	ep.On(event.EventMessage, mh.handleRoomEvent)
	ep.On(event.EventReaction, mh.handleRoomEvent)
	ep.On(event.EventEncrypted, mh.handleRoomEvent)
	ep.On(event.StateMember, mh.handleRoomEvent)
	ep.On(event.StateCreate, mh.handleRoomEvent)
	ep.On(event.StateAliases, mh.handleRoomEvent)
	ep.On(event.StateCanonicalAlias, mh.handleRoomEvent)
	ep.On(event.StateRoomName, mh.handleRoomEvent)
	as.Router.HandleFunc("/ws/{roomId}", mh.wsHandler())
	as.Router.HandleFunc("/", indexHandler(roomRepository))
	as.Router.HandleFunc("/api/rooms", listRoomsHandler(roomRepository))

	go as.Start()
	log.Println("AppService started")

	ep.Start(context.Background())
	log.Println("EventProcessor started")

	go mh.broadcastMessages()

	as.Ready = true

	if err != nil {
		log.Fatal(err)
	}

	log.Println(resp.UserID)

	exitCode := WaitForInterrupt()
	as.Stop()
	os.Exit(exitCode)
}

type Message struct {
	Body   string `json:"body"`
	User   string `json:"user"`
	RoomID string `json:"room_id"`
}

type MessageHandler struct {
	matrixMessages chan Message
	client         *mautrix.Client
	roomRepository *storage.RoomRepository
	connections    map[string][]*websocket.Conn
}

func NewMessageHandler(matrixMessages chan Message, client *mautrix.Client, roomRepository *storage.RoomRepository) *MessageHandler {
	return &MessageHandler{
		matrixMessages: matrixMessages,
		client:         client,
		roomRepository: roomRepository,
		connections:    make(map[string][]*websocket.Conn),
	}
}

func listRoomsHandler(repo *storage.RoomRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		rooms, err := repo.List()
		if err != nil {
			http.Error(w, "Failed to fetch rooms", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rooms); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	}
}

func (m *MessageHandler) handleRoomEvent(ctx context.Context, ev *event.Event) {
	log.Printf("Received room event, ID: %s, of type: %s", ev.RoomID, ev.Type)

	rawEvt := ev.Content.Raw
	log.Println(rawEvt)

	switch ev.Type {
	case event.StateCreate:
		m.roomRepository.Store(&storage.Room{
			ID:        ev.RoomID.String(),
			CreatedAt: time.Now(),
		})
	case event.StateRoomName:
		room, err := m.roomRepository.Fetch(ev.RoomID.String())
		if err != nil {
			log.Printf("Error fetching room: %v", err)
			return
		}
		room.Name = ev.Content.AsRoomName().Name
		if err := m.roomRepository.Store(room); err != nil {
			log.Printf("Error storing room: %v", err)
		}
	case event.EventMessage:
		msg := ev.Content.AsMessage().Body
		user := ev.Sender.String()
		logLine := user + ": " + msg
		log.Println(logLine)
		m.matrixMessages <- Message{Body: msg, User: user, RoomID: ev.RoomID.String()}
	}
}

func (m *MessageHandler) broadcastMessages() {
	for msg := range m.matrixMessages {
		payload, _ := json.Marshal(msg)
		roomConnections, ok := m.connections[msg.RoomID]
		if !ok {
			log.Printf("No connections for room ID: %s", msg.RoomID)
			continue
		}
		for _, conn := range roomConnections {
			err := conn.WriteMessage(websocket.TextMessage, payload)
			if err != nil {
				log.Printf("Failed to write message: %v", err)
			}
		}
	}
}

func (m *MessageHandler) wsHandler() http.HandlerFunc {

	var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Be careful with this in production
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		roomId := mux.Vars(r)["roomId"]
		log.Printf("WS request for Room ID: %s", roomId)

		if roomId == "" {
			http.Error(w, "Room ID is required", http.StatusBadRequest)
			log.Println("Room ID is required")
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		if _, ok := m.connections[roomId]; !ok {
			m.connections[roomId] = []*websocket.Conn{conn}
		} else {
			m.connections[roomId] = append(m.connections[roomId], conn)
		}

		log.Printf("Connection for room ID: %s registered\n", roomId)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				break
			}

			// Broadcast the message
			m.client.SendMessageEvent(context.Background(), id.RoomID(roomId), event.EventMessage, event.MessageEventContent{MsgType: event.MsgText, Body: string(message)})
		}

		// Remove the connection from the list
		connections := m.connections[roomId]
		for i, c := range connections {
			if c == conn {
				m.connections[roomId] = append(connections[:i], connections[i+1:]...)
				break
			}
		}
	}
}

func indexHandler(repo *storage.RoomRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rooms, err := repo.List()
		if err != nil {
			http.Error(w, "Failed to fetch rooms", http.StatusInternalServerError)
			return
		}

		tmpl := template.Must(template.ParseFiles("html/index.html"))
		tmpl.Execute(w, map[string]interface{}{
			"Rooms": rooms,
		})
	}
}

func WaitForInterrupt() int {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case <-c:
		log.Println("Interrupt signal received from OS")
		return 0
		// case exitCode := <-br.manualStop:
		// 	log.Println("Internal stop signal received")
		// 	return exitCode
	}
}
