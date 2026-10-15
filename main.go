package main

import (
	"net/http"

	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"

	"github.com/joho/godotenv"

	"github.com/smarniw/metamorphosis/src/users"
)

func hellow(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello from metamorphosis api"))
}

func main() {
	_ = godotenv.Load() // at the top of main
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	fmt.Println("metamorphosis running on port:", port)
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	// database connection pool
	databaseUrl := os.Getenv("GOOSE_DBSTRING")
	pool, err := pgxpool.New(context.Background(), databaseUrl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
	}
	defer pool.Close()
	//

	r.Get("/", hellow)

	userHandler := users.NewUserHandler(pool)
	r.Mount("/users", userHandler.UserRouter())

	http.ListenAndServe(":"+port, r)
}
