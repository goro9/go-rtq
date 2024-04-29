package rtq

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/samber/lo"
)

// Have a RoundTrip queue for each specific request, and if the request matches, retrieve the RoundTrip from the queue and execute it.
type MockTransport struct {
	queues      []*RoundTripQueue
	requestLogs []requestLog
	mu          sync.Mutex
}

var _ http.RoundTripper = (*MockTransport)(nil)

func NewTransport(queues ...RoundTripQueue) *MockTransport {
	return &MockTransport{
		queues: lo.ToSlicePtr(queues),
	}
}

func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	roundTrip, err := m.dequeue(req)
	if err != nil {
		return nil, err
	}
	return roundTrip(req)
}

func (m *MockTransport) dequeue(req *http.Request) (func(*http.Request) (*http.Response, error), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find a queue matching the request
	q, found, err := m.find(req)
	if err != nil {
		return nil, err
	}
	if !found {
		m.requestLogs = append(m.requestLogs, requestLog{matched: false, request: req})
		return nil, errors.New("mock is not registered")
	}
	m.requestLogs = append(m.requestLogs, requestLog{matched: true, request: req})
	// Retrieve the roundTrip from the queue and execute it
	// In the find method, queues with len(roundTripFuncs) of 0 are not matched, so it is guaranteed that len(roundTripFuncs) is 1 or more.
	roundTrip := q.roundTripFuncs[0]
	q.roundTripFuncs = q.roundTripFuncs[1:]

	return roundTrip, nil
}

// Find a queue that matches the passed request
func (m *MockTransport) find(req *http.Request) (*RoundTripQueue, bool, error) {
	for _, q := range m.queues {
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

func (m *MockTransport) unmatchRequests() []*http.Request {
	return lo.FilterMap(m.requestLogs, func(l requestLog, _ int) (*http.Request, bool) {
		return l.request, !l.matched
	})
}

func (m *MockTransport) Completed() bool {
	remaining := lo.SumBy(
		m.queues,
		func(q *RoundTripQueue) int { return len(q.roundTripFuncs) },
	)
	return remaining == 0 && len(m.unmatchRequests()) == 0
}

func (m *MockTransport) RequestLogString() string {
	return strings.Join(
		lo.Map(m.requestLogs, func(l requestLog, i int) string { return fmt.Sprintf("%d: %s", i+1, l.String()) }),
		"\n",
	)
}

type MatchFunc func(*http.Request) (bool, error)

// roundTrip queue
type RoundTripQueue struct {
	matchFuncs     []MatchFunc
	roundTripFuncs []func(*http.Request) (*http.Response, error)
}

func New(origin string) RoundTripQueue {
	matchFuncs := []MatchFunc{
		func(req *http.Request) (bool, error) {
			return req.URL.Scheme+"://"+req.URL.Host == origin, nil
		},
	}
	return RoundTripQueue{
		matchFuncs:     matchFuncs,
		roundTripFuncs: make([]func(*http.Request) (*http.Response, error), 0),
	}
}

func (q RoundTripQueue) match(req *http.Request) (bool, error) {
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

func (q RoundTripQueue) Header(key, value string) RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.Header.Get(key) == value, nil
	})
	return q
}

func (q RoundTripQueue) method(method string) RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.Method == method, nil
	})
	return q
}

func (q RoundTripQueue) path(path string) RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.URL.Path == path, nil
	})
	return q
}

func (q RoundTripQueue) Get(path string) RoundTripQueue {
	return q.method(http.MethodGet).path(path)
}

func (q RoundTripQueue) Post(path string) RoundTripQueue {
	return q.method(http.MethodPost).path(path)
}

func (q RoundTripQueue) Put(path string) RoundTripQueue {
	return q.method(http.MethodPut).path(path)
}

func (q RoundTripQueue) Delete(path string) RoundTripQueue {
	return q.method(http.MethodDelete).path(path)
}

func (q RoundTripQueue) Query(key, value string) RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, func(req *http.Request) (bool, error) {
		return req.URL.Query().Get(key) == value, nil
	})
	return q
}

func (q RoundTripQueue) BodyString(body string) RoundTripQueue {
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

func (q RoundTripQueue) Matcher(matchFunc MatchFunc) RoundTripQueue {
	q.matchFuncs = append(q.matchFuncs, matchFunc)
	return q
}

func (q RoundTripQueue) ResponseSimple(statusCode int, body string) RoundTripQueue {
	q.roundTripFuncs = append(q.roundTripFuncs, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	return q
}

func (q RoundTripQueue) ResponseJSON(statusCode int, body any) RoundTripQueue {
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

func (q RoundTripQueue) Response(res *http.Response) RoundTripQueue {
	q.roundTripFuncs = append(q.roundTripFuncs, func(req *http.Request) (*http.Response, error) {
		return res, nil
	})
	return q
}

func (q RoundTripQueue) ResponseFunc(roundTrip func(*http.Request) (*http.Response, error)) RoundTripQueue {
	q.roundTripFuncs = append(q.roundTripFuncs, roundTrip)
	return q
}

type requestLog struct {
	matched bool
	request *http.Request
}

func (l requestLog) String() string {
	s := fmt.Sprintf("%s %s", l.request.Method, l.request.URL.String())
	if !l.matched {
		s += " (not matched)"
	}
	return s
}
