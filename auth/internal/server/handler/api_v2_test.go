package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApiV2(t *testing.T) {
	type wantType struct {
		code int
		body string
	}
	type testType struct {
		name     string
		disable  bool
		newFunc  func() (ApiV2, *mockResourceV2Ok)
		testFunc func(t *testing.T, router ApiV2) *httptest.ResponseRecorder
		want     wantType
	}
	tests := []testType{
		{
			name: "positive #1 ApiV2 route: " + OkURL,
			newFunc: func() (ApiV2, *mockResourceV2Ok) {
				mock := &mockResourceV2Ok{}
				return NewApiV2(mock), mock
			},
			testFunc: func(t *testing.T, router ApiV2) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, OkURL, nil)
				return testServeHTTP(router, req)
			},
			want: wantType{
				code: http.StatusOK,
				body: "ok",
			},
		},
		{
			name: "positive #2 route: " + AuthURL,
			newFunc: func() (ApiV2, *mockResourceV2Ok) {
				mock := &mockResourceV2Ok{}
				return NewApiV2(mock), mock
			},
			testFunc: func(t *testing.T, router ApiV2) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, AuthURL, nil)
				return testServeHTTP(router, req)
			},
			want: wantType{
				code: http.StatusOK,
				body: "auth",
			},
		},
		{
			name: "positive #3 route: " + RefreshURL,
			newFunc: func() (ApiV2, *mockResourceV2Ok) {
				mock := &mockResourceV2Ok{}
				return NewApiV2(mock), mock
			},
			testFunc: func(t *testing.T, router ApiV2) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, RefreshURL, nil)
				return testServeHTTP(router, req)
			},
			want: wantType{
				code: http.StatusOK,
				body: "refresh",
			},
		},
		{
			name: "positive #4 route: " + RegisterURL,
			newFunc: func() (ApiV2, *mockResourceV2Ok) {
				mock := &mockResourceV2Ok{}
				return NewApiV2(mock), mock
			},
			testFunc: func(t *testing.T, router ApiV2) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, RegisterURL, nil)
				return testServeHTTP(router, req)
			},
			want: wantType{
				code: http.StatusOK,
				body: "register",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.disable {
				router, mock := tt.newFunc()
				got := tt.testFunc(t, router)
				assert.Equal(t, tt.want.code, got.Code)
				assert.Equal(t, tt.want.body, got.Body.String())
				assert.True(t, mock.called)
			}
		})
	}
}

func testServeHTTP(router ApiV2, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type mockResourceV2Ok struct {
	called bool
}

func (m *mockResourceV2Ok) Ok(w http.ResponseWriter, _ *http.Request) error {
	m.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
	return nil
}

func (m *mockResourceV2Ok) Auth(w http.ResponseWriter, _ *http.Request) error {
	m.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("auth"))
	return nil
}

func (m *mockResourceV2Ok) Refresh(w http.ResponseWriter, _ *http.Request) error {
	m.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("refresh"))
	return nil
}

func (m *mockResourceV2Ok) Register(w http.ResponseWriter, _ *http.Request) error {
	m.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("register"))
	return nil
}
