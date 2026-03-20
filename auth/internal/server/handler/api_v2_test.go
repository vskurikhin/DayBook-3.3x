package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"go.uber.org/mock/gomock"
)

func TestApiV2(t *testing.T) {
	type wantType struct {
		code int
		body string
	}
	type testType struct {
		name     string
		disable  bool
		newFunc  func(t gomock.TestReporter, opts ...gomock.ControllerOption) (ApiV2, *mockResourceV2Ok, *gomock.Controller)
		testFunc func(t *testing.T, router ApiV2) *httptest.ResponseRecorder
		want     wantType
	}
	tests := []testType{
		{
			name: "positive #1 ApiV2 route: " + OkURL,
			newFunc: func(t gomock.TestReporter, opts ...gomock.ControllerOption) (ApiV2, *mockResourceV2Ok, *gomock.Controller) {
				ctrl := gomock.NewController(t)
				cfgMock := NewMockConfig(ctrl)
				mock := &mockResourceV2Ok{}

				cfgMock.EXPECT().Values().Return(config.Values{}).AnyTimes()

				return NewApiV2(cfgMock, mock), mock, ctrl
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
			newFunc: func(t gomock.TestReporter, opts ...gomock.ControllerOption) (ApiV2, *mockResourceV2Ok, *gomock.Controller) {
				ctrl := gomock.NewController(t)
				cfgMock := NewMockConfig(ctrl)
				mock := &mockResourceV2Ok{}

				cfgMock.EXPECT().Values().Return(config.Values{}).AnyTimes()

				return NewApiV2(cfgMock, mock), mock, ctrl
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
			newFunc: func(t gomock.TestReporter, opts ...gomock.ControllerOption) (ApiV2, *mockResourceV2Ok, *gomock.Controller) {
				ctrl := gomock.NewController(t)
				cfgMock := NewMockConfig(ctrl)
				mock := &mockResourceV2Ok{}

				cfgMock.EXPECT().Values().Return(config.Values{}).AnyTimes()

				return NewApiV2(cfgMock, mock), mock, ctrl
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
			newFunc: func(t gomock.TestReporter, opts ...gomock.ControllerOption) (ApiV2, *mockResourceV2Ok, *gomock.Controller) {
				ctrl := gomock.NewController(t)
				cfgMock := NewMockConfig(ctrl)
				mock := &mockResourceV2Ok{}

				cfgMock.EXPECT().Values().Return(config.Values{}).AnyTimes()

				return NewApiV2(cfgMock, mock), mock, ctrl
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
				router, mock, ctrl := tt.newFunc(t)
				defer ctrl.Finish()
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

func (m *mockResourceV2Ok) Logout(w http.ResponseWriter, r *http.Request) error {
	m.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("logout"))
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
