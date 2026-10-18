package exercises

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/src/shared"
)

func NewExerciseHandler(db *pgxpool.Pool) *ExerciseHandler {
	return &ExerciseHandler{db: db}
}

func (h *ExerciseHandler) ExerciseRouter() *chi.Mux {
	apiRouter := chi.NewRouter()
	apiRouter.Get("/{user_id}", h.getExercisesByUserId)
	apiRouter.Post("/{user_id}", h.createNewExercise)
	return apiRouter
}

func (h *ExerciseHandler) getExercisesByUserId(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userIdParam := chi.URLParam(r, "user_id")
	rows, err := h.db.Query(
		r.Context(),
		`
		SELECT id, user_id, name, created_at, updated_at
		FROM exercises
		WHERE user_id = $1
		`,
		userIdParam,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		errors.As(err, &pgErr)
		fmt.Println("[get-exercise-by-user-id] unhandled error", err)
		http.Error(w, "failed to get exercise by user id", http.StatusInternalServerError)
		return
	}
	defer rows.Close() // why are we closing the rows?

	exercises := []Exercise{}
	for rows.Next() {
		exercise := Exercise{}
		err := rows.Scan(&exercise.ID, &exercise.UserId, &exercise.Name, &exercise.CreatedAt, &exercise.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			errors.As(err, &pgErr)
			fmt.Println("[get-exercise-by-user-id] unhandled error", err)
			http.Error(w, "failed to get exercise by user id", http.StatusInternalServerError)
			return
		}
		exercises = append(exercises, exercise)
	}
	json.NewEncoder(w).Encode(exercises)
}

func (h *ExerciseHandler) createNewExercise(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userIdParam := chi.URLParam(r, "user_id")

	var req CreateExerciseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// validate incoming request
	fmt.Println("[DEBUG]", "1")
	validationErr := req.ValidateCreateExerciseRequest()
	if validationErr != nil {
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return
	}

	var exercise Exercise
	err := h.db.QueryRow(
		r.Context(),
		`
		INSERT INTO exercises (user_id, name)
		VALUES ($1, $2)
		RETURNING id, user_id, name, created_at, updated_at
		`,
		userIdParam, req.Name,
	).Scan(&exercise.ID, &exercise.UserId, &exercise.Name, &exercise.CreatedAt, &exercise.UpdatedAt)

	if err != nil {
		fmt.Println("[create-new-exercise]", err)
		var pgErr *pgconn.PgError
		errors.As(err, &pgErr)
		if pgErr.Code == "23505" && pgErr.ConstraintName == "exercises_user_id_name_key" {
			shared.SendError(w, shared.DB_UNIQUE_CONSTRAINT_VIOLATION, "exercise already exists for user")
			return
		} else {
			fmt.Println("[create-new-exercise] unhandled error", err)
			http.Error(w, "failed to create new exercise", http.StatusInternalServerError)
			return
		}
	}
	json.NewEncoder(w).Encode(exercise)
}
