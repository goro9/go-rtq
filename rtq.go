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
type RoundTripQueues struct {
	queues          map[string][]*RoundTripQueue
	unmatchRequests []*http.Request
}

var _ http.RoundTripper = (*RoundTripQueues)(nil)

func NewTransport(origin string, queueList ...*RoundTripQueue) *RoundTripQueues {
	return &RoundTripQueues{
		queues: map[string][]*RoundTripQueue{
			origin: queueList,
		},
	}
}

func (qs *RoundTripQueues) SetMock(origin string, queueList ...*RoundTripQueue) {
	qs.queues[origin] = queueList
}

func (qs *RoundTripQueues) RoundTrip(req *http.Request) (*http.Response, error) {
	// Find a queue matching the request
	q, found, err := qs.find(req)
	if err != nil {
		return nil, err
	}
	if !found {
		qs.unmatchRequests = append(qs.unmatchRequests, req)
		return nil, errors.New("mock is not registered")
	}
	// Retrieve the roundTrip from the queue and execute it
	roundTrip := q.roundTrips[0]
	q.roundTrips = q.roundTrips[1:]
	return roundTrip(req)
}

func (qs *RoundTripQueues) Completed() bool {
	remaining := lo.SumBy(
		lo.Flatten(lo.Values(qs.queues)),
		func(q *RoundTripQueue) int { return len(q.roundTrips) },
	)
	return remaining == 0 && len(qs.unmatchRequests) == 0
}

// Find a queue that matches the passed request
func (qs *RoundTripQueues) find(req *http.Request) (*RoundTripQueue, bool, error) {
	queues, ok := qs.queues[origin(req.URL)]
	if !ok {
		return nil, false, errors.New("origin is not registered")
	}

	var merr error
	q, found := lo.Find(queues, func(q *RoundTripQueue) bool {
		// If roundTrips is empty, it is treated as no match and the next matching queue is searched.
		if len(q.roundTrips) == 0 {
			return false
		}

		m, err := q.match(req)
		if err != nil {
			merr = errors.Join(merr, err)
		}
		return m
	})

	return q, found, merr
}

func origin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

type MatchFunc func(*http.Request) (bool, error)

// roundTrip queue
type RoundTripQueue struct {
	matchFuncs []MatchFunc
	roundTrips []func(*http.Request) (*http.Response, error)
}

func New() *RoundTripQueue {
	return &RoundTripQueue{
		roundTrips: make([]func(*http.Request) (*http.Response, error), 0),
	}
}

func (q *RoundTripQueue) match(req *http.Request) (bool, error) {
	var merr error
	m := lo.EveryBy(q.matchFuncs, func(f MatchFunc) bool {
		m, err := f(req)
		if err != nil {
			merr = errors.Join(merr, err)
		}
		return m
	})
	return m, merr
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
	q.roundTrips = append(q.roundTrips, func(req *http.Request) (*http.Response, error) {
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

	q.roundTrips = append(q.roundTrips, func(req *http.Request) (*http.Response, error) {
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
	q.roundTrips = append(q.roundTrips, func(req *http.Request) (*http.Response, error) {
		return res, nil
	})
	return q
}

func (q *RoundTripQueue) ResponseFunc(roundTrip func(*http.Request) (*http.Response, error)) *RoundTripQueue {
	q.roundTrips = append(q.roundTrips, roundTrip)
	return q
}
