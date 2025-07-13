package common

import (
	"strings"
	"testing"
)

// TestNewMessage は新しいメッセージの作成をテストする
// @obj: NewMessage関数の動作確認
// @ref: SCIK9X27-000006-000001 - メッセージ構造の基本的な検証
func TestNewMessage(t *testing.T) {
	tests := []struct {
		name     string
		msgType  MessageType
		msg      string
		reqID    string
		expected *Message
	}{
		{
			name:    "JOINメッセージ",
			msgType: TypeJoin,
			msg:     "worker123",
			reqID:   "",
			expected: &Message{
				Type:  TypeJoin,
				Msg:   "worker123",
				ReqID: "",
			},
		},
		{
			name:    "REQUESTメッセージ（リクエストID付き）",
			msgType: TypeRequest,
			msg:     "タスクプロンプト",
			reqID:   "req-123",
			expected: &Message{
				Type:  TypeRequest,
				Msg:   "タスクプロンプト",
				ReqID: "req-123",
			},
		},
		{
			name:    "DONEメッセージ",
			msgType: TypeDone,
			msg:     "task_000001.txt",
			reqID:   "",
			expected: &Message{
				Type:  TypeDone,
				Msg:   "task_000001.txt",
				ReqID: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewMessage(tt.msgType, tt.msg, tt.reqID)
			
			if result.Type != tt.expected.Type {
				t.Errorf("Type mismatch: got %s, want %s", result.Type, tt.expected.Type)
			}
			if result.Msg != tt.expected.Msg {
				t.Errorf("Msg mismatch: got %s, want %s", result.Msg, tt.expected.Msg)
			}
			if result.ReqID != tt.expected.ReqID {
				t.Errorf("ReqID mismatch: got %s, want %s", result.ReqID, tt.expected.ReqID)
			}
		})
	}
}

// TestMessageMarshal はメッセージのJSON変換をテストする
// @obj: Marshal関数によるJSON形式への変換確認
// @ref: SCIK9X27-000006-000013 - TCP通信用の改行付きJSON
func TestMessageMarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  *Message
		want string
	}{
		{
			name: "シンプルなメッセージ",
			msg:  NewMessage(TypeJoin, "worker123", ""),
			want: `{"type":"JOIN","msg":"worker123"}` + "\n",
		},
		{
			name: "リクエストID付きメッセージ",
			msg:  NewMessage(TypeRequest, "タスク内容", "req-456"),
			want: `{"type":"REQUEST","msg":"タスク内容","req_id":"req-456"}` + "\n",
		},
		{
			name: "空のメッセージ",
			msg:  NewMessage(TypeCheckAck, "", "req-789"),
			want: `{"type":"CHECK_ACK","msg":"","req_id":"req-789"}` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			got := string(data)
			if got != tt.want {
				t.Errorf("Marshal result mismatch:\ngot:  %q\nwant: %q", got, tt.want)
			}

			// 改行が含まれていることを確認
			if !strings.HasSuffix(got, "\n") {
				t.Error("Marshal result should end with newline")
			}
		})
	}
}

// TestUnmarshal はJSONからメッセージへの変換をテストする
// @obj: Unmarshal関数によるJSON解析の確認
// @ref: SCIK9X27-000006-000000 - JSONメッセージの解析
func TestUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *Message
		wantErr bool
	}{
		{
			name:  "正常なJOINメッセージ",
			input: `{"type":"JOIN","msg":"worker123"}`,
			want: &Message{
				Type:  TypeJoin,
				Msg:   "worker123",
				ReqID: "",
			},
			wantErr: false,
		},
		{
			name:  "リクエストID付きメッセージ",
			input: `{"type":"REQUEST_ACK","msg":"","req_id":"req-123"}`,
			want: &Message{
				Type:  TypeRequestAck,
				Msg:   "",
				ReqID: "req-123",
			},
			wantErr: false,
		},
		{
			name:    "不正なJSON",
			input:   `{"type":"JOIN","msg":`,
			want:    nil,
			wantErr: true,
		},
		{
			name:  "改行付きJSON（実際のTCP通信形式）",
			input: `{"type":"DONE","msg":"task_001.txt"}` + "\n",
			want: &Message{
				Type:  TypeDone,
				Msg:   "task_001.txt",
				ReqID: "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Unmarshal([]byte(tt.input))
			
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if err != nil {
				return
			}

			if got.Type != tt.want.Type {
				t.Errorf("Type mismatch: got %s, want %s", got.Type, tt.want.Type)
			}
			if got.Msg != tt.want.Msg {
				t.Errorf("Msg mismatch: got %s, want %s", got.Msg, tt.want.Msg)
			}
			if got.ReqID != tt.want.ReqID {
				t.Errorf("ReqID mismatch: got %s, want %s", got.ReqID, tt.want.ReqID)
			}
		})
	}
}

// TestMessageTypes はすべてのメッセージタイプが定義されていることを確認する
// @obj: メッセージタイプの網羅性確認
// @ref: SCIK9X27-000006-000004 から SCIK9X27-000006-000012
func TestMessageTypes(t *testing.T) {
	expectedTypes := []MessageType{
		TypeJoin,
		TypeJoinAck,
		TypeRequest,
		TypeRequestAck,
		TypeDone,
		TypeFailed,
		TypeUsageLimited,
		TypeCheck,
		TypeCheckAck,
		TypeLeave,
		TypeCompleted,
		TypeTimer,
		TypeTimeoutCheck,
		TypeExit,
		TypeDisconnect,
	}

	// 各タイプが空でないことを確認
	for _, msgType := range expectedTypes {
		if msgType == "" {
			t.Errorf("Message type should not be empty")
		}
	}

	// 各タイプがユニークであることを確認
	typeMap := make(map[MessageType]bool)
	for _, msgType := range expectedTypes {
		if typeMap[msgType] {
			t.Errorf("Duplicate message type: %s", msgType)
		}
		typeMap[msgType] = true
	}
}

// TestMessageString はString()メソッドをテストする
// @obj: メッセージの文字列表現を確認
// @ref: SCIK9X27-000006-000001 - メッセージのデバッグ出力
func TestMessageString(t *testing.T) {
	msg := NewMessage(TypeRequest, "タスク内容", "req-123")
	str := msg.String()
	
	if !strings.Contains(str, "REQUEST") {
		t.Errorf("String() should contain message type")
	}
	if !strings.Contains(str, "タスク内容") {
		t.Errorf("String() should contain message content")
	}
	if !strings.Contains(str, "req-123") {
		t.Errorf("String() should contain request ID")
	}
}

// TestRoundTrip はMarshalとUnmarshalの往復変換をテストする
// @obj: シリアライズとデシリアライズの整合性確認
// @ref: SCIK9X27-000006-000000, SCIK9X27-000006-000013
func TestRoundTrip(t *testing.T) {
	messages := []*Message{
		NewMessage(TypeJoin, "worker123", ""),
		NewMessage(TypeRequest, "長いタスクプロンプト\n複数行にわたる内容", "req-789"),
		NewMessage(TypeDone, "task_999.txt", ""),
		NewMessage(TypeCheckAck, "", "check-456"),
		NewMessage(TypeUsageLimited, "worker123", ""),
	}

	for _, original := range messages {
		t.Run(original.Type.String(), func(t *testing.T) {
			// Marshal
			data, err := original.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// 改行を削除してUnmarshal
			data = []byte(strings.TrimSuffix(string(data), "\n"))
			
			// Unmarshal
			decoded, err := Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			// 比較
			if decoded.Type != original.Type {
				t.Errorf("Type mismatch after round trip: got %s, want %s", decoded.Type, original.Type)
			}
			if decoded.Msg != original.Msg {
				t.Errorf("Msg mismatch after round trip: got %s, want %s", decoded.Msg, original.Msg)
			}
			if decoded.ReqID != original.ReqID {
				t.Errorf("ReqID mismatch after round trip: got %s, want %s", decoded.ReqID, original.ReqID)
			}
		})
	}
}

// MessageType の String メソッドを追加
func (m MessageType) String() string {
	return string(m)
}