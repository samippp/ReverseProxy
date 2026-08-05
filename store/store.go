package store

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/samippp/ReverseProxy/db/generated"
	"golang.org/x/crypto/bcrypt"
)

type Store struct {
	queries *db.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{queries: db.New(pool)}
}

func (s *Store) CreateUser(ctx context.Context, username, password, email string) (db.User, error) {
	// business logic goes here (e.g. hash password)
	return s.queries.CreateUser(ctx, db.CreateUserParams{
		Username: username,
		Password: password,
		Email:    email,
	})
}

func (s *Store) RegisterUser(context context.Context, username, password, email string) (db.User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal("Cannot hash password: {}. {}", string(hashedPassword), err)
	}
	user, err := s.CreateUser(context, username, string(hashedPassword), email)
	if err != nil {
		return db.User{}, err
	}
	return user, nil
}

func (s *Store) Login(context context.Context, username, password string) (db.User, error) {
	queriedUser, err := s.GetUserByUsername(context, username)
	if err != nil {
		return db.User{}, err
	}
	// compare password for auth
	error := bcrypt.CompareHashAndPassword([]byte(queriedUser.Password), []byte(password))
	return queriedUser, error
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (db.User, error) {
	return s.queries.GetUserByUsername(ctx, username)
}

func (s *Store) DeleteUserByUserName(ctx context.Context, username string) error {
	return s.queries.DeleteUserByUserName(ctx, username)
}
