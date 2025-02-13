package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	asToken := os.Getenv("AS_TOKEN")
	hsToken := os.Getenv("HS_TOKEN")
	homeserverHost := os.Getenv("HOMESERVER_HOST")
	userID := os.Getenv("USER_ID")
	roomId := os.Getenv("ROOM_ID")

	matrixMessages := make(chan string)

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
		ID:              "FhirConnector",
		SenderLocalpart: "_fhir",
		Namespaces: appservice.Namespaces{
			UserIDs: []appservice.Namespace{{
				Regex:     "@_fhir.*",
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

	mh := &MessageHandler{matrixMessages: matrixMessages, client: client, roomId: roomId}

	ep := appservice.NewEventProcessor(as)
	ep.On(event.EventMessage, mh.handleRoomEvent)
	ep.On(event.EventReaction, mh.handleRoomEvent)
	ep.On(event.EventEncrypted, mh.handleRoomEvent)
	ep.On(event.StateMember, mh.handleRoomEvent)
	as.Router.HandleFunc("/ws", mh.wsHandler())
	as.Router.HandleFunc("/", indexHandler)

	go as.Start()
	log.Println("AppService started")

	ep.Start(context.Background())
	log.Println("EventProcessor started")

	as.Ready = true

	if err != nil {
		log.Fatal(err)
	}

	log.Println(resp.UserID)

	exitCode := WaitForInterrupt()
	as.Stop()
	os.Exit(exitCode)
}

type MessageHandler struct {
	matrixMessages chan string
	client         *mautrix.Client
	roomId         string
}

func (m *MessageHandler) handleRoomEvent(ctx context.Context, ev *event.Event) {
	log.Printf("Received room event of type: %s", ev.Type)
	if ev.Type == event.EventMessage {
		msg := ev.Content.AsMessage().Body
		user := ev.Sender.String()
		logLine := user + ": " + msg
		log.Println(logLine)
		m.matrixMessages <- logLine
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
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		go func() {
			// Keep connection alive and send messages
			for msg := range m.matrixMessages {
				err := conn.WriteMessage(websocket.TextMessage, []byte(msg))
				if err != nil {
					log.Printf("Failed to write message: %v", err)
					return
				}
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				break
			}

			// Broadcast the message
			m.client.SendMessageEvent(context.Background(), id.RoomID(m.roomId), event.EventMessage, event.MessageEventContent{MsgType: event.MsgText, Body: string(message)})
		}
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("html/index.html"))
	tmpl.Execute(w, nil)
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
