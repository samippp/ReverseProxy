// Package api for business logic with api endpoints
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/samippp/ReverseProxy/auth"
	"github.com/samippp/ReverseProxy/config"
	generated "github.com/samippp/ReverseProxy/db/generated"
	"github.com/samippp/ReverseProxy/store"
)

type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

type Handler struct {
	store     *store.Store
	envConfig *config.EnvConfig
}

func New(store *store.Store) Handler {
	env := config.LoadEnv()
	return Handler{store, env}
}

func (h *Handler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	defer fmt.Println("Response delivered")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message" : "This is homepage"}`))
}

func (h *Handler) RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var createUser CreateUserRequest

	if err := json.NewDecoder(r.Body).Decode(&createUser); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	user, err := h.store.RegisterUser(r.Context(), createUser.Username, createUser.Password, createUser.Email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	writeUser(w, user)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var loginUser CreateUserRequest

	if err := json.NewDecoder(r.Body).Decode(&loginUser); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}

	context, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	user, err := h.store.Login(context, loginUser.Username, loginUser.Password)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
	}
	defer r.Body.Close()

	token, expiresIn, err := auth.GenerateToken(user.ID, user.Username, h.envConfig.JwtSecret)
	if err != nil {
		log.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"token":      token,
		"expires_in": strconv.FormatInt(expiresIn, 10),
	})
}

func writeUser(w http.ResponseWriter, user generated.User) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"password":   user.Password,
		"email":      user.Email,
		"created_at": user.CreatedAt.Time,
	})
}

func (h *Handler) CheckUserExist(ctx context.Context, username string) (generated.User, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	user, err := h.store.GetUserByUsername(ctx, username)
	if err != nil {
		fmt.Println("User not found {}", username)
		return generated.User{}, err
	}
	return user, nil
}

func (h *Handler) DeleteUser(ctx context.Context, username string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := h.store.DeleteUserByUserName(ctx, username)
	return err
}

func (h *Handler) SecretHandler(w http.ResponseWriter, r *http.Response) error {
	return nil
}
