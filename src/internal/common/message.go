package common

import (
	"encoding/json"
	"fmt"
)

// MessageType はメッセージタイプを表す
// @obj メッセージのタイプを定義
// @ref SCIK9X27-000006-000004
type MessageType string

const (
	// @obj 参入メッセージタイプ
	// @ref SCIK9X27-000006-000005
	TypeJoin MessageType = "JOIN"
	
	// @obj 参入応答メッセージタイプ
	// @ref SCIK9X27-000006-000006
	TypeJoinAck MessageType = "JOIN_ACK"
	
	// @obj タスク実行依頼メッセージタイプ
	// @ref SCIK9X27-000006-000007
	TypeRequest MessageType = "REQUEST"
	
	// @obj タスク実行依頼承諾メッセージタイプ
	// @ref SCIK9X27-000006-000008
	TypeRequestAck MessageType = "REQUEST_ACK"
	
	// @obj タスク完了報告メッセージタイプ
	// @ref SCIK9X27-000006-000009
	TypeDone MessageType = "DONE"
	
	// @obj タスク失敗報告メッセージタイプ
	// @ref SCIK9X27-000006-00000A
	TypeFailed MessageType = "FAILED"
	
	// @obj レートリミット失敗報告メッセージタイプ
	// @ref SCIK9X27-000006-00000B
	TypeUsageLimited MessageType = "USAGE_LIMITED"
	
	// @obj ヘルスチェックメッセージタイプ
	// @ref SCIK9X27-000006-00000C
	TypeCheck MessageType = "CHECK"
	
	// @obj ヘルスチェック応答メッセージタイプ
	// @ref SCIK9X27-000006-00000D
	TypeCheckAck MessageType = "CHECK_ACK"
	
	// @obj 離脱メッセージタイプ
	// @ref SCIK9X27-000006-00000E
	TypeLeave MessageType = "LEAVE"
	
	// @obj 全タスク完了メッセージタイプ
	// @ref SCIK9X27-000006-00000F
	TypeCompleted MessageType = "COMPLETED"
	
	// @obj タイマーイベントメッセージタイプ
	// @ref SCIK9X27-000006-000010
	TypeTimer MessageType = "TIMER"
	
	// @obj ワーカー応答タイムアウトチェックメッセージタイプ
	// @ref SCIK9X27-000006-000011
	TypeTimeoutCheck MessageType = "TIMEOUT_CHECK"
	
	// @obj プログラム停止メッセージタイプ
	// @ref SCIK9X27-000006-000012
	TypeExit MessageType = "EXIT"
	
	// @obj 切断通知メッセージタイプ
	// @ref SCIK9X27-000006-000016
	TypeDisconn MessageType = "DISCONN"
	
	// @obj 切断完了メッセージタイプ（内部使用）
	TypeDisconnect MessageType = "DISCONNECT"
)

// Message はメッセージの基本構造を表す
// @obj タスク管理マスタとタスクワーカー間、スレッド間でやり取りするメッセージ
// @ref SCIK9X27-000006-000000, SCIK9X27-000006-000001
type Message struct {
	Type     MessageType `json:"type"`
	Msg      string      `json:"msg"`
	ReqID    string      `json:"req_id,omitempty"`
	TaskFile string      `json:"task_file,omitempty"` // タスクファイル名を追加
}

// NewMessage は新しいメッセージを作成する
// @obj メッセージインスタンスの生成
// @ref SCIK9X27-000006-000001, SCIK9X27-000006-000003
func NewMessage(msgType MessageType, msg string, reqID string) *Message {
	return &Message{
		Type:  msgType,
		Msg:   msg,
		ReqID: reqID,
	}
}

// NewTaskMessage はタスクファイル名付きのメッセージを作成する
// @obj REQUESTmesgメッセージ用のタスクファイル名付きメッセージ生成
// @ref SCIK9X27-000003-00003E
func NewTaskMessage(msgType MessageType, msg string, reqID string, taskFile string) *Message {
	return &Message{
		Type:     msgType,
		Msg:      msg,
		ReqID:    reqID,
		TaskFile: taskFile,
	}
}

// Marshal はメッセージをJSON形式にシリアライズする
// @obj メッセージをTCP通信用にJSON形式に変換（改行付き）
// @ref SCIK9X27-000006-000013
func (m *Message) Marshal() ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	// TCP通信用に改行を付与
	return append(data, '\n'), nil
}

// Unmarshal はJSON形式のバイト列からメッセージを復元する
// @obj TCP通信で受信したJSONデータをメッセージ構造体に変換
// @ref SCIK9X27-000006-000000
func Unmarshal(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// String はメッセージの文字列表現を返す
func (m *Message) String() string {
	return fmt.Sprintf("Type: %s, Msg: %s, ReqID: %s", m.Type, m.Msg, m.ReqID)
}