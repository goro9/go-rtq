# go-rtq

A Go library for mocking HTTP requests with a queue-based RoundTrip implementation. Useful for testing HTTP clients without making real network calls.

API design and signatures reference [gock](https://pkg.go.dev/github.com/h2non/gock).

## Features

- **Queue-based mocking**: Each request pattern has its own queue of mock responses; when a request matches, the response is consumed from that queue in FIFO order
- **Flexible request matching**: Match by origin, method, path, headers, query parameters, and request body
- **Multiple response types**: Return simple text, JSON, custom `*http.Response`, or custom handler functions
- **Thread-safe**: Safe for concurrent use in parallel tests

## Installation

```bash
go get github.com/goro9/go-rtq
```

## Usage

Add the following imports to your test file:

```go
import (
    "io"
    "net/http"
    "strings"
    "testing"

    "github.com/goro9/go-rtq"
)
```

### Basic Example

```go
func TestBasicExample(t *testing.T) {
    q := rtq.New("http://api.example.com").
        Get("/users").
        ResponseSimple(200, `{"users": []}`).
        ResponseJSON(200, map[string]any{"users": []})

    transport := rtq.NewTransport(q)
    client := http.Client{Transport: transport}

    // First request returns {"users": []}
    resp1, err := client.Get("http://api.example.com/users")
    if err != nil {
        t.Fatal(err)
    }
    defer resp1.Body.Close()

    // Second request returns JSON response
    resp2, err := client.Get("http://api.example.com/users")
    if err != nil {
        t.Fatal(err)
    }
    defer resp2.Body.Close()

    // Third request fails with "mock is not registered"
    _, err = client.Get("http://api.example.com/users")
    if err == nil {
        t.Error("expected error: mock is not registered")
    }
}
```

### Request Matching

Match requests by various criteria:

```go
func TestRequestMatching(t *testing.T) {
    transport := rtq.NewTransport(
        rtq.New("http://api.example.com").Get("/users").ResponseSimple(200, "OK"),
        rtq.New("http://api.example.com").Post("/users").ResponseSimple(201, "Created"),
        rtq.New("http://api.example.com").
            Get("/users").
            Header("Authorization", "Bearer token123").
            ResponseSimple(200, "OK"),
        rtq.New("http://api.example.com").
            Get("/users").
            Query("page", "1").
            ResponseSimple(200, "OK"),
        rtq.New("http://api.example.com").
            Post("/users").
            BodyString(`{"name":"John"}`).
            ResponseSimple(200, "OK"),
        rtq.New("http://api.example.com").
            Matcher(func(req *http.Request) (bool, error) {
                return req.URL.Path == "/custom", nil
            }).
            ResponseSimple(200, "OK"),
    )
    client := http.Client{Transport: transport}
    // ... make requests and assert
}
```

### Response Types

```go
func TestResponseTypes(t *testing.T) {
    transport := rtq.NewTransport(
        rtq.New("http://api.example.com").
            Get("/simple").
            ResponseSimple(200, "OK"),
        rtq.New("http://api.example.com").
            Get("/json").
            ResponseJSON(200, map[string]any{"id": 1, "name": "Alice"}),
        rtq.New("http://api.example.com").
            Post("/custom").
            Response(&http.Response{
                StatusCode: 201,
                Body:       io.NopCloser(strings.NewReader("Created")),
            }),
        rtq.New("http://api.example.com").
            Get("/func").
            ResponseFunc(func(req *http.Request) (*http.Response, error) {
                return &http.Response{
                    StatusCode: 200,
                    Body:       io.NopCloser(strings.NewReader("custom")),
                    Request:    req,
                }, nil
            }),
    )
    client := http.Client{Transport: transport}
    // ... make requests and assert
}
```

### Multiple Queues

Register multiple queues for different request patterns:

```go
func TestMultipleQueues(t *testing.T) {
    transport := rtq.NewTransport(
        rtq.New("http://api.example.com").
            Get("/users").
            ResponseSimple(200, `{"users": []}`),
        rtq.New("http://api.example.com").
            Post("/users").
            ResponseJSON(201, map[string]any{"id": 1}),
        rtq.New("http://other.com").
            Get("/health").
            ResponseSimple(200, "OK"),
    )
    client := http.Client{Transport: transport}
    // ... make requests and assert
}
```

### Verification

Check if all mocks were consumed and no unexpected requests were made:

```go
func TestVerification(t *testing.T) {
    transport := rtq.NewTransport(
        rtq.New("http://api.example.com").Get("/users").ResponseSimple(200, "OK"),
    )
    client := http.Client{Transport: transport}

    resp, err := client.Get("http://api.example.com/users")
    if err != nil {
        t.Fatal(err)
    }
    defer resp.Body.Close()

    if !transport.Completed() {
        t.Error("expected all mocks to be consumed")
    }

    // Get request log string for debugging
    t.Log(transport.RequestLogString())
}
```

## License

See LICENSE file for details.
