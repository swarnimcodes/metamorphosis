package users

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
)

func NewUserHandler(db *pgxpool.Pool) *UserHandler {
	return &UserHandler{db: db}
}

func (h *UserHandler) UserRouter() *chi.Mux {
	apiRouter := chi.NewRouter()
	apiRouter.Get("/id/{id}", h.getUserById)
	apiRouter.Get("/username/{username}", h.getUserByUsername)
	apiRouter.Post("/", h.createNewUser)
	return apiRouter
}

func (h *UserHandler) getUserByUsername(w http.ResponseWriter, r *http.Request) {
	usernameParam := chi.URLParam(r, "username")

	var user User
	err := h.db.QueryRow(
		r.Context(),
		`SELECT id, email, first_name, last_name, username, created_at, updated_at
                 FROM users
                 WHERE username = $1
                `,
		usernameParam,
	).Scan(&user.ID, &user.Email, &user.FirstName, &user.LastName, &user.Username, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "no such user found", http.StatusNotFound)
			return
		}

		fmt.Println("[get-user-by-username]", err)
		http.Error(w, "failed to fetch user by that username", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (h *UserHandler) createNewUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	validationErr := req.ValidateCreateUserRequest()
	if validationErr != nil {
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return
	}

	var user User
	err := h.db.QueryRow(
		r.Context(),
		`INSERT INTO users (email, first_name, last_name, username)
                 VALUES ($1, $2, $3, $4)
                 RETURNING id, email, first_name, last_name, username, created_at, updated_at`,
		req.Email, req.FirstName, req.LastName, req.Username,
	).Scan(&user.ID, &user.Email, &user.FirstName, &user.LastName, &user.Username, &user.CreatedAt, &user.UpdatedAt)

	// check error sent by postgres
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		fmt.Println("[create-new-user]", pgErr.ConstraintName)
		switch pgErr.ConstraintName {
		case "users_email_key":
			http.Error(w, "email already in use", http.StatusConflict)
		case "users_username_key":
			http.Error(w, "username already in use", http.StatusConflict)
		default:
			http.Error(w, "duplicate key (unexpected state tbh)", http.StatusConflict)
		}
		return

	} // we are just handling unique constraints as of now
	// as we understand more, we should probably handle other expected cases too

	if err != nil {
		fmt.Println("create user error", err)
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)

}

func (h *UserHandler) getUserById(w http.ResponseWriter, r *http.Request) {
	userIdParam := chi.URLParam(r, "id")

	var user User
	err := h.db.QueryRow(
		r.Context(),
		`SELECT id, email, first_name, last_name, username, created_at, updated_at
                 FROM users
                 WHERE id = $1
                `,
		userIdParam,
	).Scan(&user.ID, &user.Email, &user.FirstName, &user.LastName, &user.Username, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "no such user found", http.StatusNotFound)
			return
		}

		fmt.Println("[get-user-by-id]", err)
		http.Error(w, "failed to fetch user by that id", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}
