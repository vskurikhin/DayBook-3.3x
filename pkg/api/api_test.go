package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestJSONWireFormat фиксирует проводной формат REST API: для каждой
// сериализуемой структуры эталонный экземпляр кодируется в JSON,
// результат посимвольно сравнивается с эталонной строкой, созданной напрямую, а эталон
// декодируется обратно и сверяется по значениям полей. Порядок ключей
// encoding/json совпадает с порядком полей структуры, поэтому эталоны
// детерминированы.
func TestJSONWireFormat(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		subject any
	}{
		{
			name:    "PutRequest",
			want:    `{"Key":"k","Value":"v"}`,
			subject: PutRequest{Key: "k", Value: "v"},
		},
		{
			name:    "PutResponse",
			want:    `{"RespStatus":1,"KeyFound":true,"PrevValue":"prev"}`,
			subject: PutResponse{RespStatus: StatusOK, KeyFound: true, PrevValue: "prev"},
		},
		{
			name:    "GetRequest",
			want:    `{"Key":"k"}`,
			subject: GetRequest{Key: "k"},
		},
		{
			name:    "GetResponse",
			want:    `{"RespStatus":1,"KeyFound":true,"Value":"val"}`,
			subject: GetResponse{RespStatus: StatusOK, KeyFound: true, Value: "val"},
		},
		{
			name:    "CASRequest",
			want:    `{"Key":"k","CompareValue":"c","Value":"v"}`,
			subject: CASRequest{Key: "k", CompareValue: "c", Value: "v"},
		},
		{
			name:    "CASResponse",
			want:    `{"RespStatus":1,"KeyFound":false,"PrevValue":"prev"}`,
			subject: CASResponse{RespStatus: StatusOK, KeyFound: false, PrevValue: "prev"},
		},
		{
			name:    "StatusResponse",
			want:    `{"RespStatus":2}`,
			subject: StatusResponse{RespStatus: StatusNotLeader},
		},
		{
			name:    "DeleteRequest",
			want:    `{"Key":"k"}`,
			subject: DeleteRequest{Key: "k"},
		},
		{
			name:    "DeleteResponse",
			want:    `{"RespStatus":1,"KeyFound":true,"PrevValue":"prev"}`,
			subject: DeleteResponse{RespStatus: StatusOK, KeyFound: true, PrevValue: "prev"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.subject)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("Marshal mismatch:\n got  %s\n want %s", got, tt.want)
			}

			decoded := reflect.New(reflect.TypeOf(tt.subject)).Interface()
			if err := json.Unmarshal([]byte(tt.want), decoded); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(reflect.ValueOf(decoded).Elem().Interface(), tt.subject) {
				t.Fatalf("Unmarshal round-trip mismatch:\n got  %#v\n want %#v",
					reflect.ValueOf(decoded).Elem().Interface(), tt.subject)
			}
		})
	}
}

// TestResponseStatusString фиксирует значения String() всех статусов
// и возврат пустой строки для неизвестного значения — контракт
// fmt-печати, сохраняемый при любом изменении реализации.
func TestResponseStatusString(t *testing.T) {
	tests := []struct {
		name string
		rs   ResponseStatus
		want string
	}{
		{"StatusInvalid", StatusInvalid, "invalid"},
		{"StatusOK", StatusOK, "OK"},
		{"StatusNotLeader", StatusNotLeader, "NotLeader"},
		{"StatusFailedCommit", StatusFailedCommit, "FailedCommit"},
		{"unknown", ResponseStatus(42), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rs.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
