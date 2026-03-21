package resources

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services"
	"go.uber.org/mock/gomock"
)

func TestV2_Ok(t *testing.T) {
	tests := []struct {
		name         string
		serviceResp  string
		expectedBody string
	}{
		{
			name:         "success",
			serviceResp:  "ok",
			expectedBody: `{"success":true,"data":[{"msg":"ok"}]}` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockAuthServiceV2(ctrl)
			mockSvc.EXPECT().Ok().Return(tt.serviceResp)

			v := NewV2(mockSvc)

			req := httptest.NewRequest(http.MethodGet, "/v2/ok", nil)
			rec := httptest.NewRecorder()

			err := v.Ok(rec, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if rec.Body.String() != tt.expectedBody {
				t.Fatalf("expected %s, got %s", tt.expectedBody, rec.Body.String())
			}
		})
	}
}

func TestV2_Auth(t *testing.T) {
	tests := []struct {
		name        string
		body        any
		mockSetup   func(m *MockAuthServiceV2)
		expectError bool
	}{
		{
			name:        "invalid json",
			body:        "invalid",
			mockSetup:   func(m *MockAuthServiceV2) {},
			expectError: true,
		},
		//{
		//	name: "service error",
		//	body: dto.Login{},
		//	mockSetup: func(m *MockAuthServiceV2) {
		//		m.EXPECT().
		//			Auth(gomock.Any(), gomock.Any()).
		//			Return(nil, errors.New("fail"))
		//	},
		//	expectError: true,
		//},
		{
			name: "success",
			body: dto.Login{},
			mockSetup: func(m *MockAuthServiceV2) {
				m.EXPECT().
					Auth(gomock.Any(), gomock.Any()).
					Return(mockCredentials(), nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockAuthServiceV2(ctrl)
			tt.mockSetup(mockSvc)

			v := NewV2(mockSvc)

			var bodyBytes []byte
			switch b := tt.body.(type) {
			case string:
				bodyBytes = []byte(b)
			default:
				bodyBytes, _ = json.Marshal(b)
			}

			req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(bodyBytes))
			rec := httptest.NewRecorder()

			err := v.Auth(rec, req)

			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestV2_Refresh(t *testing.T) {
	tests := []struct {
		name        string
		withCookie  bool
		mockSetup   func(m *MockAuthServiceV2)
		expectError bool
	}{
		{
			name:        "no cookie",
			withCookie:  false,
			mockSetup:   func(m *MockAuthServiceV2) {},
			expectError: true,
		},
		//{
		//	name:       "service error",
		//	withCookie: true,
		//	mockSetup: func(m *MockAuthServiceV2) {
		//		m.EXPECT().
		//			Refresh(gomock.Any(), "token").
		//			Return(nil, errors.New("fail"))
		//	},
		//	expectError: true,
		//},
		{
			name:       "success",
			withCookie: true,
			mockSetup: func(m *MockAuthServiceV2) {
				m.EXPECT().
					Refresh(gomock.Any(), "token").
					Return(mockCredentials(), nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockAuthServiceV2(ctrl)
			tt.mockSetup(mockSvc)

			v := NewV2(mockSvc)

			req := httptest.NewRequest(http.MethodPost, "/refresh", bytes.NewReader([]byte(`{}`)))
			if tt.withCookie {
				req.AddCookie(&http.Cookie{Name: "refresh", Value: "token"})
			}

			rec := httptest.NewRecorder()

			err := v.Refresh(rec, req)

			if tt.expectError && err == nil {
				t.Fatal("expected error")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestV2_Logout(t *testing.T) {
	tests := []struct {
		name        string
		mockSetup   func(m *MockAuthServiceV2)
		expectError bool
	}{
		{
			name: "success",
			mockSetup: func(m *MockAuthServiceV2) {
				m.EXPECT().Logout(gomock.Any()).Return(nil)
			},
		},
		{
			name: "error",
			mockSetup: func(m *MockAuthServiceV2) {
				m.EXPECT().Logout(gomock.Any()).Return(errors.New("fail"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockAuthServiceV2(ctrl)
			tt.mockSetup(mockSvc)

			v := NewV2(mockSvc)

			req := httptest.NewRequest(http.MethodPost, "/logout", nil)
			rec := httptest.NewRecorder()

			err := v.Logout(rec, req)

			if tt.expectError && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestV2_Register(t *testing.T) {
	tests := []struct {
		name        string
		body        any
		mockSetup   func(m *MockAuthServiceV2)
		expectError bool
	}{
		{
			name:        "invalid json",
			body:        "bad",
			mockSetup:   func(m *MockAuthServiceV2) {},
			expectError: true,
		},
		{
			name: "success",
			body: dto.CreateUser{},
			mockSetup: func(m *MockAuthServiceV2) {
				m.EXPECT().
					Register(gomock.Any(), gomock.Any()).
					Return(mockCredentials(), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockAuthServiceV2(ctrl)
			tt.mockSetup(mockSvc)

			v := NewV2(mockSvc)

			var bodyBytes []byte
			switch b := tt.body.(type) {
			case string:
				bodyBytes = []byte(b)
			default:
				bodyBytes, _ = json.Marshal(b)
			}

			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(bodyBytes))
			rec := httptest.NewRecorder()

			err := v.Register(rec, req)

			if tt.expectError && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func mockCredentials() services.Credentials {
	return services.Credentials{}
}
