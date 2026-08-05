package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samippp/ReverseProxy/api"
	"github.com/samippp/ReverseProxy/auth"
	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/db"
	"github.com/samippp/ReverseProxy/proxy"
	"github.com/samippp/ReverseProxy/store"
)

var (
	testPool   *pgxpool.Pool
	testStore  *store.Store
	testServer *httptest.Server
)

func TestMain(m *testing.M) {
	env := config.LoadEnv()
	testPool = db.PostgreSQLConnectionPoll(env.DatabaseURL)
	testStore = store.New(testPool)
	router := proxy.NewRouter(testPool)
	testServer = httptest.NewServer(router)

	// run all tests
	code := m.Run()

	// teardown
	testPool.Close()

	os.Exit(code)
}

func TestLoadConfig(t *testing.T) {
	c := config.ImportConfig()
	fmt.Println(c)
	fmt.Println("---------Config Test-------------")
}

func TestHomeRouteWithServer(t *testing.T) {
	fmt.Println("------------------Test 1-----------------")
	resp, err := http.Get(testServer.URL + "/home")
	if err != nil {
		t.Fatalf("Failed to get response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 but got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}

func TestRegisterWithServer(t *testing.T) {
	fmt.Println("------------------Test 2-----------------")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// create user from json request
	user := api.CreateUserRequest{Username: "John", Password: "meow", Email: "john@gmail.com"}
	payloadToSend, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("Failed to marshal user: %v", err)
	}

	// call api
	resp, err := login(payloadToSend)
	if err != nil {
		t.Fatalf("Failed to get response from test server: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	fmt.Println(string(body))
	defer testStore.DeleteUserByUserName(ctx, "John")

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 but got %d", resp.StatusCode)
	}

	if u, err := testStore.GetUserByUsername(ctx, "John"); err != nil {
		t.Error("User not found in db")
	} else {
		fmt.Println(u)
	}
	testStore.DeleteUserByUserName(ctx, "John")
}

func login(payloadToSend []byte) (*http.Response, error) {
	resp, err := http.Post(testServer.URL+"/register", "application/json",
		bytes.NewBuffer(payloadToSend))
	return resp, err
}

func TestLoginWithServer(t *testing.T) {
	fmt.Println("------------------Test 3-----------------")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	user := api.CreateUserRequest{Username: "John", Password: "meow", Email: "john@gmail.com"}
	payloadToSend, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("Failed to marshal user: %v", err)
	}
	login(payloadToSend)
	defer testStore.DeleteUserByUserName(ctx, "John")
	// create user from json request
	resp, err := http.Post(testServer.URL+"/login", "application/json",
		bytes.NewBuffer(payloadToSend))
	if err != nil {
		t.Error(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	fmt.Println(string(body))
	var data map[string]string
	json.Unmarshal(body, &data)
	tokenStr := data["token"]
	claims, err := auth.ValidateToken(tokenStr)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(claims)
}
