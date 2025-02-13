package storage

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"time"
)

type Room struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type RoomRepository struct {
	db *sql.DB
}

func NewRoomRepository(db *sql.DB) *RoomRepository {
	return &RoomRepository{db: db}
}

// InitDB initializes the database schema
func (r *RoomRepository) InitDB() error {
	query := `
    CREATE TABLE IF NOT EXISTS rooms (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        created_at DATETIME NOT NULL,
        updated_at DATETIME NOT NULL
    );`

	_, err := r.db.Exec(query)
	return err
}

// Store saves a room to the database
func (r *RoomRepository) Store(room *Room) error {
	query := `
    INSERT INTO rooms (id, name, created_at, updated_at)
    VALUES (?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
        name = excluded.name,
        updated_at = excluded.updated_at`

	now := time.Now()
	if room.CreatedAt.IsZero() {
		room.CreatedAt = now
	}
	room.UpdatedAt = now

	_, err := r.db.Exec(query, room.ID, room.Name, room.CreatedAt, room.UpdatedAt)
	return err
}

// Fetch retrieves a room by ID
func (r *RoomRepository) Fetch(id string) (*Room, error) {
	query := `
    SELECT id, name, created_at, updated_at
    FROM rooms
    WHERE id = ?`

	room := &Room{}
	err := r.db.QueryRow(query, id).Scan(
		&room.ID,
		&room.Name,
		&room.CreatedAt,
		&room.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return room, nil
}

// SearchByName searches for rooms by name using a LIKE query
func (r *RoomRepository) SearchByName(name string) ([]Room, error) {
	query := `
        SELECT id, name, created_at, updated_at
        FROM rooms
        WHERE name LIKE ?
        ORDER BY created_at DESC`

	// Add wildcards to the search term
	searchTerm := "%" + name + "%"

	rows, err := r.db.Query(query, searchTerm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var room Room
		err := rows.Scan(
			&room.ID,
			&room.Name,
			&room.CreatedAt,
			&room.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

// List retrieves all rooms
func (r *RoomRepository) List() ([]Room, error) {
	query := `
    SELECT id, name, created_at, updated_at
    FROM rooms
    ORDER BY created_at DESC`

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var room Room
		err := rows.Scan(
			&room.ID,
			&room.Name,
			&room.CreatedAt,
			&room.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}
