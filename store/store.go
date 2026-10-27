// Package store manages users in PostgreSQL, including password hashing.
package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"runtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/samippp/ReverseProxy/db/generated"
	"golang.org/x/crypto/bcrypt"
)

// Store wraps the sqlc queries.
type Store struct {
	queries *db.Queries
	// bcryptSlots caps concurrent bcrypt (~50ms CPU each) so logins
	// can't starve proxied traffic.
	bcryptSlots chan struct{}
	// dummyHash is checked for unknown users, so they take as long as a
	// wrong password and timing doesn't reveal usernames.
	dummyHash []byte
}

// New returns a Store using pool.
func New(pool *pgxpool.Pool) *Store {
	dummyHash, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("cannot create dummy password hash: %v", err)
	}
	return &Store{
		queries:     db.New(pool),
		bcryptSlots: make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2)),
		dummyHash:   dummyHash,
	}
}

// ErrBusy is returned when ctx ends while waiting for a bcrypt slot.
var ErrBusy = errors.New("too many concurrent password operations")

// withBcryptSlot runs f once a bcrypt slot is free, or returns ErrBusy
// if ctx ends first.
func (s *Store) withBcryptSlot(ctx context.Context, f func()) error {
	select {
	case s.bcryptSlots <- struct{}{}:
	case <-ctx.Done():
		return ErrBusy
	}
	defer func() { <-s.bcryptSlots }()
	f()
	return nil
}

// CreateUser inserts a user with an already-hashed password.
func (s *Store) CreateUser(ctx context.Context, username, password, email string) (db.User, error) {
	return s.queries.CreateUser(ctx, db.CreateUserParams{
		Username: username,
		Password: password,
		Email:    email,
	})
}

// RegisterUser hashes password and creates the user.
func (s *Store) RegisterUser(context context.Context, username, password, email string) (db.User, error) {
	var hashedPassword []byte
	var err error
	if slotErr := s.withBcryptSlot(context, func() {
		hashedPassword, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	}); slotErr != nil {
		return db.User{}, slotErr
	}
	if err != nil {
		return db.User{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.CreateUser(context, username, string(hashedPassword), email)
	if err != nil {
		return db.User{}, err
	}
	return user, nil
}

// ErrInvalidCredentials means an unknown username or a wrong password.
var ErrInvalidCredentials = errors.New("invalid username or password")

// Login returns the user if password matches.
func (s *Store) Login(context context.Context, username, password string) (db.User, error) {
	queriedUser, err := s.GetUserByUsername(context, username)
	userExists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, err
	}
	hash := s.dummyHash
	if userExists {
		hash = []byte(queriedUser.Password)
	}
	if slotErr := s.withBcryptSlot(context, func() {
		err = bcrypt.CompareHashAndPassword(hash, []byte(password))
	}); slotErr != nil {
		return db.User{}, slotErr
	}
	if !userExists || errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return db.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return db.User{}, err
	}
	return queriedUser, nil
}

// GetUserByUsername looks up a user.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (db.User, error) {
	return s.queries.GetUserByUsername(ctx, username)
}

// DeleteUserByUserName deletes a user.
func (s *Store) DeleteUserByUserName(ctx context.Context, username string) error {
	return s.queries.DeleteUserByUserName(ctx, username)
}
