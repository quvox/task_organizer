package common

import (
	"encoding/json"
	"fmt"
)

/*
@obj: メッセージタイプの定義
@ref: IPS3MKEQ-000004-000004 "メッセージタイプには以下のものがある。"
*/
const (
	// @ref: IPS3MKEQ-000004-000005 "JOIN: 参入"
	MessageTypeJoin = "JOIN"
	// @ref: IPS3MKEQ-000004-000006 "JOIN_ACK: 参入応答"
	MessageTypeJoinAck = "JOIN_ACK"
	// @ref: IPS3MKEQ-000004-000007 "REQUEST: タスク実行依頼"
	MessageTypeRequest = "REQUEST"
	// @ref: IPS3MKEQ-000004-000008 "REQUEST_ACK: タスク実行依頼承諾"
	MessageTypeRequestAck = "REQUEST_ACK"
	// @ref: IPS3MKEQ-000004-000009 "DONE: タスク完了報告"
	MessageTypeDone = "DONE"
	// @ref: IPS3MKEQ-000004-00000A "FAILED: タスク失敗報告"
	MessageTypeFailed = "FAILED"
	// @ref: IPS3MKEQ-000004-00000B "USAGE_LIMITED: タスク失敗報告（レートリミットに引っかかった）"
	MessageTypeUsageLimited = "USAGE_LIMITED"
	// @ref: IPS3MKEQ-000004-00000C "CHECK: ヘルスチェック"
	MessageTypeCheck = "CHECK"
	// @ref: IPS3MKEQ-000004-00000D "CHECK_ACK: ヘルスチェック応答"
	MessageTypeCheckAck = "CHECK_ACK"
	// @ref: IPS3MKEQ-000004-00000E "LEAVE: 離脱"
	MessageTypeLeave = "LEAVE"
	// @ref: IPS3MKEQ-000004-00000F "COMPLETED: 全タスク完了"
	MessageTypeCompleted = "COMPLETED"
	// @ref: IPS3MKEQ-000004-000010 "TIMER: タイマーイベント"
	MessageTypeTimer = "TIMER"
	// @ref: IPS3MKEQ-000004-000011 "TIMEOUT_CHECK: ワーカー応答タイムアウトのチェック"
	MessageTypeTimeoutCheck = "TIMEOUT_CHECK"
	// @ref: IPS3MKEQ-000004-000012 "EXIT: プログラム停止（ループ終了）"
	MessageTypeExit = "EXIT"
	// @ref: IPS3MKEQ-000002-00002B "DISCONNECT: 切断通知"
	MessageTypeDisconnect = "DISCONNECT"
)

/*
@obj: メッセージ構造体の定義
@ref: IPS3MKEQ-000004-000000 "タスク管理マスタとタスクワーカー間、および同一ツール内のスレッドと待ち受けループ間でやり取りするメッセージは、以下に示すように、key-valueペアをJSON形式にしたものとする。"
@ref: IPS3MKEQ-000004-000001 '{ "type": "<メッセージタイプ>", "msg": "<メッセージ内容>" }'
@ref: IPS3MKEQ-000004-000003 '{ "type": "<メッセージタイプ>", "msg": "<メッセージ内容>", "req_id": "<リクエストID>" }'
*/
type Message struct {
	Type  string `json:"type"`
	Msg   string `json:"msg"`
	ReqID string `json:"req_id,omitempty"`
}

/*
@obj: メッセージをJSON形式にシリアライズする
@ref: IPS3MKEQ-000004-000000 "key-valueペアをJSON形式にしたものとする"
*/
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

/*
@obj: JSON形式からメッセージをデシリアライズする
@ref: IPS3MKEQ-000004-000000 "key-valueペアをJSON形式にしたものとする"
*/
func UnmarshalMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message: %w", err)
	}
	return &m, nil
}