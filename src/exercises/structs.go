package exercises

import (
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ExerciseHandler struct {
	db *pgxpool.Pool
}

type Exercise struct {
	ID        uuid.UUID `json:"id"`
	UserId    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateExerciseRequest struct {
	// UserId uuid.UUID `json:"user_id"`
	Name string `json:"name"`
}
