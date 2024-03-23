package rtq

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/samber/lo"
)

// Have a RoundTrip queue for each specific request, and if the request matches, retrieve the RoundTrip from the queue and execute it.
type MockTransport struct {
	queuesByOrigin  map[string][]*RoundTripQueue
	unmatchRequests []*http.Request
}

var _ http.RoundTripper = (*MockTransport)(nil)

func NewTransport(origin string, queueList ...*RoundTripQueue) *MockTransport {
	return &MockTransport{
		queuesByOrigin: map[string][]*RoundTripQueue{
			origin: queueList,
		},
	}
}

func (m *MockTransport) SetMock(origin string, queueList ...*RoundTripQueue) {
	m.queuesByOrigin[origin] = queueList
}

func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Find a queue matching the request
	q, found, err := m.find(req)
	if err != nil {
		return nil, err
	}
	if !found {
		m.unmatchRequests = append(m.unmatchRequests, req)
		return nil, errors.New("mock is not registered")
	}
	if len(q.roundTripFuncs) == 0 {
		return nil, errors.New("queue is found but it's empty")
	}
	// Retrieve the roundTrip from the queue and execute it
	roundTrip := q.roundTripFuncs[0]
	q.roundTripFuncs = q.roundTripFuncs[1:]
	return roundTrip(req)
}

func (m *MockTransport) Completed() bool {
	remaining := lo.SumBy(
		lo.Flatten(lo.Values(m.queuesByOrigin)),
		func(q *RoundTripQueue) int { return len(q.roundTripFuncs) },
	)
	return remaining == 0 && len(m.unmatchRequests) == 0
}

// Find a queue that matches the passed request
func (m *MockTransport) find(req *http.Request) (*RoundTripQueue, bool, error) {
	queues, ok := m.queuesByOrigin[origin(req.URL)]
	if !ok {
		return nil, false, errors.New("origin is not registered")
	}

	for _, q := range queues {
		// If roundTripFuncs is empty, it is treated as no match and the next matching queue is searched.
		if len(q.roundTripFuncs) != 0 {
			m, err := q.match(req)
			if err != nil {
				return nil, false, err
			}
			if m {
				return q, true, nil
			}
		}
	}

	return nil, false, nil
}

func origin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

type MatchFunc func(*http.Request) (bool, error)

// roundTrip queue
type RoundTripQueue struct {
	matchFuncs     []MatchFunc
	roundTripFuncs []func(*http.Request) (*http.Response, error)
}

func New() *RoundTripQueue {
	return &RoundTripQueue{
		roundTripFuncs: make([]func(*http.Request) (*http.Response, error), 0),
	}
}

func (q *RoundTripQueue) match(req *http.Request) (bool, error) {
	for _, f := range q.matchFuncs {
		m, err := f(req)
		if err != nil {
			return false, err
		}
		if !m {
			return false, nil
		}
	}
	return true, nil
}

func (q *RoundTripQueue) Header(key, value string) *RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.Header.Get(key) == value, nil
	})
	return q
}

func (q *RoundTripQueue) method(method string) *RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.Method == method, nil
	})
	return q
}

func (q *RoundTripQueue) path(path string) *RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.URL.Path == path, nil
	})
	return q
}

func (q *RoundTripQueue) Get(path string) *RoundTripQueue {
	return q.method(http.MethodGet).path(path)
}

func (q *RoundTripQueue) Post(path string) *RoundTripQueue {
	return q.method(http.MethodPost).path(path)
}

func (q *RoundTripQueue) Put(path string) *RoundTripQueue {
	return q.method(http.MethodPut).path(path)
}

func (q *RoundTripQueue) Delete(path string) *RoundTripQueue {
	return q.method(http.MethodDelete).path(path)
}

func (q *RoundTripQueue) Query(key, value string) *RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.URL.Query().Get(key) == value, nil
	})
	return q
}

func (q *RoundTripQueue) BodyString(body string) *RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		got, err := io.ReadAll(req.Body)
		if err != nil {
			return false, err
		}
		req.Body = io.NopCloser(bytes.NewReader(got))
		return string(got) == body, nil
	})
	return q
}

func (q *RoundTripQueue) Matcher(matchFunc MatchFunc) *RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, matchFunc)
	return q
}

func (q *RoundTripQueue) ResponseSimple(statusCode int, body string) *RoundTripQueue {
	q.roundTripFuncs = append(q.roundTripFuncs, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	return q
}

func (q *RoundTripQueue) ResponseJSON(statusCode int, body any) *RoundTripQueue {
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}

	q.roundTripFuncs = append(q.roundTripFuncs, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(bytes.NewBuffer(b)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})
	return q
}

func (q *RoundTripQueue) Response(res *http.Response) *RoundTripQueue {
	q.roundTripFuncs = append(q.roundTripFuncs, func(req *http.Request) (*http.Response, error) {
		return res, nil
	})
	return q
}

func (q *RoundTripQueue) ResponseFunc(roundTrip func(*http.Request) (*http.Response, error)) *RoundTripQueue {
	q.roundTripFuncs = append(q.roundTripFuncs, roundTrip)
	return q
}
