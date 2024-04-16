package rtq

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/samber/lo"
)

func TestMockTransport(t *testing.T) {
	mockTransport := NewTransport("http://example.com",
		New().
			ResponseSimple(200, `{"count": 1}`).
			ResponseSimple(200, `{"count": 2}`),
		New().Post("/2/sample").
			ResponseSimple(200, `{"count": 4}`),
		New().Header("Authorization", "Bearer test").Get("/2/sample").
			ResponseSimple(200, `{"count": 3}`),
		New().Query("test", "hoge").
			ResponseSimple(200, `{"count": 5}`),
		New().BodyString(`{"test":"hoge"}`).
			ResponseSimple(200, `{"count": 6}`),
	)
	mockTransport.SetMock("http://example2.com",
		New().ResponseSimple(200, `{"count": 1}`),
	)

	client := http.Client{Transport: mockTransport}

	type testExpect struct {
		Status int
		Body   string
		Error  string
	}
	specs := []struct {
		Header http.Header
		Method string
		URL    string
		Body   string
		Expect testExpect
	}{
		{
			Method: "GET",
			URL:    "http://example.com/1/sample",
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 1}`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/1/sample",
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 2}`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/1/sample",
			Expect: testExpect{
				Error: `Get "http://example.com/1/sample": mock is not registered`,
			},
		},
		{
			Header: http.Header{
				"Authorization": []string{"Bearer test"},
			},
			Method: "GET",
			URL:    "http://example.com/1/sample",
			Expect: testExpect{
				Error: `Get "http://example.com/1/sample": mock is not registered`,
			},
		},
		{
			Header: http.Header{
				"Authorization": []string{"Bearer invalid"},
			},
			Method: "GET",
			URL:    "http://example.com/2/sample",
			Expect: testExpect{
				Error: `Get "http://example.com/2/sample": mock is not registered`,
			},
		},
		{
			Header: http.Header{
				"Authorization": []string{"Bearer test"},
			},
			Method: "GET",
			URL:    "http://example.com/2/sample",
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 3}`,
			},
		},
		{
			Method: "POST",
			URL:    "http://example.com/2/sample",
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 4}`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/3/sample?test=fuga",
			Expect: testExpect{
				Error: `Get "http://example.com/3/sample?test=fuga": mock is not registered`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/3/sample?test=hoge",
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 5}`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/4/sample",
			Body:   `{"test":"fuga"}`,
			Expect: testExpect{
				Error: `Get "http://example.com/4/sample": mock is not registered`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/4/sample",
			Body:   `{"test":"hoge"}`,
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 6}`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example.com/1/sample",
			Expect: testExpect{
				Error: `Get "http://example.com/1/sample": mock is not registered`,
			},
		},
		{
			Method: "GET",
			URL:    "http://example2.com/1/sample",
			Expect: testExpect{
				Status: 200,
				Body:   `{"count": 1}`,
			},
		},
	}

	for _, spec := range specs {
		t.Run(fmt.Sprintf("%s %s", spec.Method, spec.URL), func(t *testing.T) {
			req := lo.Must1(http.NewRequest(spec.Method, spec.URL, bytes.NewBufferString(spec.Body)))
			req.Header = spec.Header
			res, err := client.Do(req)
			if err != nil {
				if spec.Expect.Error != "" {
					if diff := cmp.Diff(spec.Expect.Error, err.Error()); diff != "" {
						t.Errorf("unexpected error: %s", diff)
					}
					return
				}
				t.Fatal(err)
			}
			got := testExpect{
				Status: res.StatusCode,
				Body:   string(lo.Must1(io.ReadAll(res.Body))),
			}
			if diff := cmp.Diff(spec.Expect, got); diff != "" {
				t.Errorf("unexpected response: %s", diff)
			}
		})
	}

	{
		expect := `1: GET http://example.com/1/sample
2: GET http://example.com/1/sample
3: GET http://example.com/1/sample (not matched)
4: GET http://example.com/1/sample (not matched)
5: GET http://example.com/2/sample (not matched)
6: GET http://example.com/2/sample
7: POST http://example.com/2/sample
8: GET http://example.com/3/sample?test=fuga (not matched)
9: GET http://example.com/3/sample?test=hoge
10: GET http://example.com/4/sample (not matched)
11: GET http://example.com/4/sample
12: GET http://example.com/1/sample (not matched)
13: GET http://example2.com/1/sample`
		if diff := cmp.Diff(expect, mockTransport.RequestLogString()); diff != "" {
			t.Errorf("unexpected request logs: %s", diff)
		}
	}
	if e, g := 6, len(mockTransport.unmatchRequests()); e != g {
		t.Errorf("unexpected unmatchRequests length: expected %d, got %d", e, g)
	}
	mockTransport.requestLogs = nil
	if !mockTransport.Completed() {
		t.Errorf("mockTransport is not empty")
	}
}

func TestMockTransportParallel(t *testing.T) {
	queue1 := New()
	for i := 0; i < 100; i++ {
		queue1 = queue1.ResponseSimple(200, fmt.Sprintf(`{"queue_index":1,"count":%d}`, i))
	}
	queue2 := New()
	for i := 0; i < 100; i++ {
		queue2 = queue2.ResponseSimple(200, fmt.Sprintf(`{"queue_index":2,"count":%d}`, i))
	}
	cnt := 100 + 100

	var wg sync.WaitGroup
	wg.Add(cnt)

	mockTransport := NewTransport("http://example.com", queue1, queue2)

	client := http.Client{Transport: mockTransport}

	for i := 0; i < cnt; i++ {
		req := lo.Must1(http.NewRequest("GET", fmt.Sprintf("http://example.com/request%d", i), nil))
		go func() {
			res, err := client.Do(req)
			if err != nil {
				t.Error(err)
			}
			t.Log(res.StatusCode, string(lo.Must1(io.ReadAll(res.Body))))
			wg.Done()
		}()
	}
	wg.Wait()
	if !mockTransport.Completed() {
		t.Error("mockTransport is not empty")
	}
	t.Log(mockTransport.RequestLogString())
}
