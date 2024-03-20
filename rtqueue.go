package rtqueue

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

func NewTransport() *RoundTripQueues {
	return &RoundTripQueues{
		queues: map[string][]*RoundTripQueue{},
	}
}

func (qs *RoundTripQueues) SetMock(origin string, queueList ...*RoundTripQueue) {
	qs.queues[origin] = queueList
}

func (qs *RoundTripQueues) RoundTrip(req *http.Request) (*http.Response, error) {
	// Find a queue matching the request
	q, ok := qs.find(req)
	if !ok {
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
func (qs *RoundTripQueues) find(req *http.Request) (*RoundTripQueue, bool) {
	return lo.Find(qs.queues[origin(req.URL)], func(q *RoundTripQueue) bool {
		// Matches if the request contains all the headers in the queue.
		for k := range q.request.header {
			if q.request.header.Get(k) != req.Header.Get(k) {
				return false
			}
		}
		// Matches if the request contains all queries in the queue.
		reqQuery := req.URL.Query()
		for k := range q.request.query {
			if q.request.query.Get(k) != reqQuery.Get(k) {
				return false
			}
		}

		if q.request.method != nil && *q.request.method != req.Method {
			return false
		}
		if q.request.url != nil && q.request.url.Path != req.URL.Path {
			return false
		}
		if !q.matchBody(req) {
			return false
		}
		// No match if queue is empty
		if len(q.roundTrips) == 0 {
			return false
		}
		return true
	})
}

func origin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// roundTrip queue
type RoundTripQueue struct {
	request    httpRequest
	roundTrips []func(*http.Request) (*http.Response, error)
}

func New() *RoundTripQueue {
	return &RoundTripQueue{
		request: httpRequest{
			header: make(http.Header),
			query:  make(url.Values),
		},
		roundTrips: make([]func(*http.Request) (*http.Response, error), 0),
	}
}

func (q *RoundTripQueue) Header(key, value string) *RoundTripQueue {
	q.request.header.Set(key, value)
	return q
}

func (q *RoundTripQueue) method(method string) *RoundTripQueue {
	q.request.method = &method
	return q
}

func (q *RoundTripQueue) path(path string) *RoundTripQueue {
	q.request.url = &url.URL{Path: path}
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
	q.request.query.Set(key, value)
	return q
}

func (q *RoundTripQueue) BodyString(body string) *RoundTripQueue {
	q.request.body = strings.NewReader(body)
	return q
}

func (q *RoundTripQueue) matchBody(req *http.Request) bool {
	if q.request.body == nil {
		return true
	}

	// TODO streaming?
	bl, err := io.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}
	req.Body = io.NopCloser(bytes.NewBuffer(bl))

	br, err := io.ReadAll(q.request.body)
	if err != nil {
		panic(err)
	}
	q.request.body = io.NopCloser(bytes.NewBuffer(br))

	return bytes.Equal(bl, br)
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

type httpRequest struct {
	header http.Header
	query  url.Values
	method *string
	url    *url.URL
	body   io.Reader
}
