package master

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

/*
@obj: LEAVEメッセージを処理する
@ref: IPS3MKEQ-000002-000027 "メインループがタスクワーカーから離脱メッセージ（LEAVE）を受信した場合、またはタスクワーカーとのTCP接続が切れた場合、該当するワーカーオブジェクトに対してワーカー切断処理を実施する。"
*/
func (m *Master) handleLeave(msg WorkerMessage) {
	m.workersMu.RLock()
	worker, exists := m.workers[msg.WorkerID]
	m.workersMu.RUnlock()

	if !exists {
		return
	}

	m.disconnectWorker(worker)
}

/*
@obj: ワーカー切断処理を実行する
@ref: IPS3MKEQ-000002-000028 "ワーカー切断処理は、ワーカーオブジェクトの稼働状態がworkingだった場合、.tasks/working/以下にある実施中のプロンプトファイルを、.tasks/pending/に移動する。"
*/
func (m *Master) disconnectWorker(worker *Worker) {
	worker.mu.Lock()
	
	/*
	@obj: 実行中のタスクをpendingに戻す
	@ref: IPS3MKEQ-000002-000028 "ワーカーオブジェクトの稼働状態がworkingだった場合、.tasks/working/以下にある実施中のプロンプトファイルを、.tasks/pending/に移動する。"
	*/
	if worker.state == StateWorking && worker.workingFile != "" {
		workingPath := filepath.Join(m.config.RootDir, ".tasks", "working", worker.workingFile)
		pendingPath := filepath.Join(m.config.RootDir, ".tasks", "pending", worker.workingFile)
		
		if err := os.Rename(workingPath, pendingPath); err != nil {
			log.Printf("Failed to move task back to pending: %v", err)
		}
	}

	/*
	@obj: 稼働状態をdisconnectingに設定する
	@ref: IPS3MKEQ-000002-000028 "その時点での稼働状態に関わらず、稼働状態をexitingにセットし、ワーカーオブジェクトを引数としてワーカー切断スレッドを起動する。"
	*/
	worker.state = StateDisconnecting
	workerID := worker.id
	conn := worker.conn
	worker.mu.Unlock()

	/*
	@obj: ワーカー切断スレッドを起動する
	@ref: IPS3MKEQ-000002-000029 "ワーカー切断スレッドは、ワーカーオブジェクト内のソケット情報を使って、ワーカーとのTCP接続を切断する。"
	*/
	go m.disconnectWorkerThread(workerID, conn)
}

/*
@obj: ワーカー切断スレッドを実行する
@ref: IPS3MKEQ-000002-000029 "ワーカー切断スレッドは、ワーカーオブジェクト内のソケット情報を使って、ワーカーとのTCP接続を切断する。切断が完了したら、メインループに、切断メッセージ（DISCONNECT）を送信し、ワーカー切断スレッドを終了する。"
@ref: IPS3MKEQ-000002-00002A "ワーカー切断スレッドには切断タイムアウトを設け、5秒たっても戻ってこなかった場合も、強制的にメインループに切断メッセージ（DISCONNECT）を送信し、ワーカー切断スレッドを終了する。"
*/
func (m *Master) disconnectWorkerThread(workerID string, conn net.Conn) {
	/*
	@obj: タイムアウト付きでコネクションを切断する
	@ref: IPS3MKEQ-000002-00002A "ワーカー切断スレッドには切断タイムアウトを設け、5秒たっても戻ってこなかった場合"
	*/
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		conn.Close()
		close(done)
	}()

	select {
	case <-done:
		// 正常に切断完了
	case <-ctx.Done():
		// タイムアウト
		log.Printf("Timeout while disconnecting worker %s", workerID)
	}

	/*
	@obj: 切断メッセージを送信する
	@ref: IPS3MKEQ-000002-000029 "切断メッセージ（DISCONNECT）を送信し、ワーカー切断スレッドを終了する。切断メッセージには、ワーカーIDを載せる。"
	*/
	m.msgChan <- WorkerMessage{
		WorkerID: workerID,
		Message: &common.Message{
			Type: common.MessageTypeDisconnect,
			Msg:  workerID,
		},
	}
}

/*
@obj: DISCONNECTメッセージを処理する
@ref: IPS3MKEQ-000002-00002B "メインループがDISCONNECTメッセージを受信したら、そこに書かれているワーカーIDを持つワーカーオブジェクトを削除する。"
*/
func (m *Master) handleDisconnect(msg WorkerMessage) {
	m.workersMu.Lock()
	defer m.workersMu.Unlock()

	workerID := msg.Message.Msg
	if _, exists := m.workers[workerID]; exists {
		delete(m.workers, workerID)
		log.Printf("Worker %s disconnected", workerID)
	}
}


/*
@obj: タスク管理マスタの終了処理を実行する
@ref: IPS3MKEQ-000002-000034 "タスク管理マスタ終了処理では、合計稼働時間、全タスク数、成功タスク数、失敗タスク数を表示するとともに、全てのTCPコネクションを切断し、タスク管理マスタを終了する。"
@ref: IPS3MKEQ-000002-000035 "コネクションの切断タイムアウトを10秒とし、タイムアウトしたら、プログラムを強制終了する。切断タイムアウトをブロックしないように注意する。"
*/
func (m *Master) shutdown() error {
	log.Println("Shutting down task master...")

	/*
	@obj: 統計情報を表示する
	@ref: IPS3MKEQ-000002-000034 "合計稼働時間、全タスク数、成功タスク数、失敗タスク数を表示する"
	*/
	duration := time.Since(m.startTime)
	m.stats.mu.Lock()
	log.Printf("Statistics:")
	log.Printf("  Total runtime: %v", duration)
	log.Printf("  Total tasks: %d", m.stats.TotalTasks)
	log.Printf("  Successful tasks: %d", m.stats.SuccessTasks)
	log.Printf("  Failed tasks: %d", m.stats.FailedTasks)
	m.stats.mu.Unlock()

	/*
	@obj: リスナーを閉じる
	*/
	if m.listener != nil {
		m.listener.Close()
	}

	/*
	@obj: 全ワーカーとの接続を切断する
	@ref: IPS3MKEQ-000002-000034 "全てのTCPコネクションを切断し"
	@ref: IPS3MKEQ-000002-000035 "コネクションの切断タイムアウトを10秒とし"
	*/
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdownDone := make(chan struct{})
	go func() {
		/*
		@obj: 全ワーカーに切断通知を送る
		*/
		m.workersMu.RLock()
		for _, worker := range m.workers {
			// 切断通知は送らず、直接接続を閉じる
			worker.conn.Close()
		}
		m.workersMu.RUnlock()

		// connMapもクリーンアップ
		m.connMu.Lock()
		for _, conn := range m.connMap {
			conn.Close()
		}
		m.connMap = make(map[string]net.Conn)
		m.connMu.Unlock()

		/*
		@obj: 全てのゴルーチンの終了を待つ
		*/
		m.wg.Wait()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		log.Println("Task master shutdown completed")
		return nil
	case <-ctx.Done():
		/*
		@obj: タイムアウト時の処理
		@ref: IPS3MKEQ-000002-000035 "タイムアウトしたら、プログラムを強制終了する。"
		@ref: IPS3MKEQ-000002-000036 "タスク管理マスタ終了処理中にCtrl-C（SIG_TERM）が発生したら、即座にプログラムを終了する。"
		*/
		log.Println("Shutdown timeout reached, forcing exit")
		return ctx.Err()
	}
}