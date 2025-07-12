package master

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

/*
@obj: タイマーイベントを処理する
@ref: IPS3MKEQ-000002-00002C "タイマーイベントメッセージを受信すると、以下の処理を実施する。"
*/
func (m *Master) handleTimer() {
	/*
	@obj: ファイル数をチェックする
	@ref: IPS3MKEQ-000002-00002D ".tasks/pending/および.tasks/working/以下のファイル数のチェック"
	@ref: IPS3MKEQ-000002-00002F "ファイル数のチェックでは、.tasks/pending/と.tasks/working/以下のファイル数を数え、ファイル数の合計が0だった場合、COMPLETEDメッセージをメインループに送信する。"
	*/
	pendingCount := m.countFiles(filepath.Join(m.config.RootDir, ".tasks", "pending"))
	workingCount := m.countFiles(filepath.Join(m.config.RootDir, ".tasks", "working"))

	if pendingCount == 0 && workingCount == 0 {
		m.msgChan <- WorkerMessage{
			Message: &common.Message{
				Type: common.MessageTypeCompleted,
			},
		}
		return
	}

	/*
	@obj: ワーカーの接続状態をチェックする
	@ref: IPS3MKEQ-000002-00002E "タスクワーカーの接続状態のチェック"
	@ref: IPS3MKEQ-000002-000030 "タスクワーカーの接続状態のチェックは、参入しているワーカーそれぞれに対してヘルスチェックメッセージを送る。"
	*/
	m.sendHealthChecks()
}

/*
@obj: ディレクトリ内のファイル数を数える
@ref: IPS3MKEQ-000002-00002D ".tasks/pending/および.tasks/working/以下のファイル数のチェック"
*/
func (m *Master) countFiles(dir string) int {
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	count := 0
	for _, file := range files {
		if !file.IsDir() {
			count++
		}
	}
	return count
}

/*
@obj: ヘルスチェックメッセージを送信する
@ref: IPS3MKEQ-000002-000030 "タスクワーカーの接続状態のチェックは、参入しているワーカーそれぞれに対してヘルスチェックメッセージを送る。"
*/
func (m *Master) sendHealthChecks() {
	m.workersMu.RLock()
	defer m.workersMu.RUnlock()

	for _, worker := range m.workers {
		/*
		@obj: CHECKメッセージを作成する
		@ref: IPS3MKEQ-000002-000030 "ヘルスチェックメッセージはtype=CHECK、req_idはリクエストID（乱数）とする。"
		@ref: IPS3MKEQ-000000-000039 'CHECK: { "type": "CHECK", "msg": "", "req_id": "<リクエストID>" }'
		*/
		requestID := fmt.Sprintf("check_%d_%d", time.Now().Unix(), rand.Int())
		check := &common.Message{
			Type:  common.MessageTypeCheck,
			Msg:   "",
			ReqID: requestID,
		}

		/*
		@obj: リクエスト情報を追加する
		@ref: IPS3MKEQ-000002-000031 "それぞれのワーカーにヘルスチェックメッセージを送信する際に、対象のワーカーオブジェクトのリクエスト情報として、リクエストIDと現在時刻＋3秒のタイムアウト時刻を追記する。"
		*/
		worker.mu.Lock()
		worker.requests = append(worker.requests, RequestInfo{
			RequestID: requestID,
			Timeout:   time.Now().Add(3 * time.Second),
		})
		worker.mu.Unlock()

		/*
		@obj: CHECKメッセージを送信する
		@ref: IPS3MKEQ-000002-000032 "送信先となるワーカーとのコネクションが切断していて、ヘルスチェックの送信に失敗してしまった場合は、そのワーカーオブジェクトに対してワーカー切断処理を実施する。"
		*/
		if err := m.sendMessage(worker.conn, check); err != nil {
			log.Printf("Failed to send health check to worker %s: %v", worker.id, err)
			m.disconnectWorker(worker)
			continue
		}

		/*
		@obj: タイムアウト監視スレッドを起動する
		@ref: IPS3MKEQ-000002-000031 "そのリクエスト情報を引数として、タイムアウト監視スレッドを起動する。"
		*/
		go m.startTimeoutMonitor(worker.id, requestID)
	}
}

/*
@obj: CHECK_ACKメッセージを処理する
@ref: IPS3MKEQ-000002-000033 "メインループがヘルスチェックの応答（CHECK_ACK）を受信すると、メッセージに書かれたリクエストIDをもつワーカーオブジェクトを取得し、該当するリクエストIDを持つリクエスト情報を削除する。"
*/
func (m *Master) handleCheckAck(msg WorkerMessage) {
	m.workersMu.RLock()
	defer m.workersMu.RUnlock()

	worker, exists := m.workers[msg.WorkerID]
	if !exists {
		return
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	/*
	@obj: リクエスト情報を削除する
	@ref: IPS3MKEQ-000002-000033 "該当するリクエストIDを持つリクエスト情報を削除する。"
	*/
	for i := 0; i < len(worker.requests); i++ {
		if worker.requests[i].RequestID == msg.Message.ReqID {
			worker.requests = append(worker.requests[:i], worker.requests[i+1:]...)
			break
		}
	}
}

/*
@obj: タイムアウト監視スレッドを開始する
@ref: IPS3MKEQ-000002-000021 "タイムアウト監視スレッドは、指定された秒数だけ経ったらタイムアウト確認メッセージ（TIMEOUT_CHECK）をメインループに送信し、処理を終了する。"
*/
func (m *Master) startTimeoutMonitor(workerID, requestID string) {
	/*
	@obj: 3秒待機する
	@ref: IPS3MKEQ-000002-00003B "REQUESTメッセージ受信後5秒間は、メッセージ受信チェックを高頻度（1ミリ秒間隔）で実施"
	*/
	// 最初の5秒間は高頻度でチェック
	endTime := time.Now().Add(3 * time.Second)
	checkInterval := 1 * time.Millisecond
	
	for time.Now().Before(endTime) {
		time.Sleep(checkInterval)
		
		// 5秒経過したら通常の間隔に戻す
		if time.Since(endTime.Add(-3*time.Second)) > 5*time.Second {
			checkInterval = 100 * time.Millisecond
		}
	}

	/*
	@obj: タイムアウト確認メッセージを送信する
	@ref: IPS3MKEQ-000002-000021 "タイムアウト確認メッセージ（TIMEOUT_CHECK）をメインループに送信し、処理を終了する。なお、タイムアウト確認メッセージには、リクエストIDを載せる。"
	*/
	m.msgChan <- WorkerMessage{
		WorkerID: workerID,
		Message: &common.Message{
			Type:  common.MessageTypeTimeoutCheck,
			ReqID: requestID,
		},
	}
}

/*
@obj: タイムアウトチェックメッセージを処理する
@ref: IPS3MKEQ-000002-000022 "メインループがタイムアウト確認メッセージ（TIMEOUT_CHECK）を受け取ったら、ワーカーオブジェクトの中から、タイムアウト確認メッセージに含まれているリクエストIDをリクエスト情報の中に持つものを取得する。"
*/
func (m *Master) handleTimeoutCheck(msg WorkerMessage) {
	m.workersMu.RLock()
	defer m.workersMu.RUnlock()

	worker, exists := m.workers[msg.WorkerID]
	if !exists {
		return
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	/*
	@obj: リクエスト情報を検索する
	@ref: IPS3MKEQ-000002-000039 "3. TIMEOUT_CHECK受信時"
	@ref: IPS3MKEQ-000002-00003A "リクエスト情報が存在しない場合（ACKで既に削除済み）→ 何もしない"
	@ref: IPS3MKEQ-000002-00003B "リクエスト情報が存在する場合（ACK未受信）→ タイムアウト判定して切断処理"
	*/
	found := false
	var reqInfo RequestInfo
	for i, req := range worker.requests {
		if req.RequestID == msg.Message.ReqID {
			found = true
			reqInfo = req
			// リクエスト情報を削除
			worker.requests = append(worker.requests[:i], worker.requests[i+1:]...)
			break
		}
	}

	if !found {
		// 既にACKで削除済み
		return
	}

	/*
	@obj: タイムアウトを判定する
	@ref: IPS3MKEQ-000002-000022 "現在時刻が該当するリクエスト情報のタイムアウト時刻を過ぎていれば、該当するリクエスト情報を配列から削除し、ワーカーオブジェクトに対してワーカー切断処理を実施する。"
	*/
	if time.Now().After(reqInfo.Timeout) {
		log.Printf("Worker %s timed out on request %s", worker.id, msg.Message.ReqID)
		worker.mu.Unlock()
		m.disconnectWorker(worker)
		return
	}
}