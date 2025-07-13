package master

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

// setupSignalHandler はシグナルハンドラーを設定する
// @obj: Ctrl-Cなどのシグナルを処理してグレースフルシャットダウンを実行。二度目のシグナルで強制終了
// @ref: SCIK9X27-000003-00001F - Ctrl-C（SIG_TERM）の処理
func (tm *TaskMaster) setupSignalHandler() {
	sigChan := make(chan os.Signal, 2) // バッファサイズを増やして二度目のシグナルを受け取る
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		// 初回のシグナル
		<-sigChan
		tm.logger.Info("Received first shutdown signal, starting graceful shutdown...")
		tm.cancel()
		
		// 二度目のシグナル監視開始
		go func() {
			<-sigChan
			tm.logger.Warn("Received second shutdown signal, forcing immediate exit")
			os.Exit(1)
		}()
	}()
}

// handleShutdown はシャットダウン処理を実行する
// @obj: マスターの終了処理を統括
// @ref: SCIK9X27-000003-00001E, SCIK9X27-000003-00001F
func (tm *TaskMaster) handleShutdown() {
	tm.logger.Info("Starting graceful shutdown...")

	// @obj: .tasks/working/以下に実施中のプロンプトファイルが残っていれば、それらをすべて.tasks/pending/に移動する
	// @ref: SCIK9X27-000003-000034
	tm.moveWorkingTasksToPending()

	// @obj: 統計情報の表示
	// @ref: SCIK9X27-000003-000034
	tm.displayStatistics()

	// リスナーはすでにメインスレッドで閉じられている

	// 全ワーカーに切断通知を送信
	tm.sendDisconnectToAllWorkers()

	// ワーカーの切断を待つ（最大5秒）
	tm.waitForWorkersDisconnect(5 * time.Second)

	// ゴルーチンの終了はメインスレッドで待機する（デッドロック回避）
	tm.logger.Info("Shutdown preparations complete")

	tm.logger.Info("Shutdown complete")
}

// moveWorkingTasksToPending は.tasks/working/のファイルを.tasks/pending/に移動する
// @obj: 終了処理時に実行中タスクを未処理タスクに戻す
// @ref: SCIK9X27-000003-000034
func (tm *TaskMaster) moveWorkingTasksToPending() {
	workingDir := filepath.Join(tm.rootDir, ".tasks", "working")
	pendingDir := filepath.Join(tm.rootDir, ".tasks", "pending")
	
	tm.logger.Infof("Moving remaining working tasks to pending directory")
	
	// workingディレクトリの内容を取得
	entries, err := os.ReadDir(workingDir)
	if err != nil {
		tm.logger.Errorf("Failed to read working directory: %v", err)
		return
	}
	
	movedCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		taskFile := entry.Name()
		workingPath := filepath.Join(workingDir, taskFile)
		pendingPath := filepath.Join(pendingDir, taskFile)
		
		tm.logger.Infof("Moving task file: %s from working to pending", taskFile)
		
		if err := os.Rename(workingPath, pendingPath); err != nil {
			tm.logger.Errorf("Failed to move task file %s from working to pending: %v", taskFile, err)
		} else {
			tm.logger.Infof("Successfully moved task file: %s", taskFile)
			movedCount++
		}
	}
	
	tm.logger.Infof("Moved %d task files from working to pending", movedCount)
}

// sendDisconnectToAllWorkers は全ワーカーに切断通知を送信する
// @obj: 接続中の全ワーカーに終了を通知し、各ワーカーに対して切断スレッドを起動
// @ref: SCIK9X27-000003-00001E, SCIK9X27-000003-000029, SCIK9X27-000003-000030
func (tm *TaskMaster) sendDisconnectToAllWorkers() {
	tm.workersMu.RLock()
	workers := make([]*WorkerInfo, 0, len(tm.workers))
	for _, worker := range tm.workers {
		workers = append(workers, worker)
	}
	tm.workersMu.RUnlock()

	for _, worker := range workers {
		// @obj: ワーカーに対して、切断通知メッセージ（DISCONN）を送信する
		// @ref: SCIK9X27-000003-00003F
		disconnectMsg := common.NewMessage(common.TypeDisconn, "Master is shutting down", "")
		if err := tm.sendMessage(worker.Conn, disconnectMsg); err != nil {
			tm.logger.Errorf("Failed to send disconnect message to worker %s: %v", worker.ID, err)
		} else {
			tm.logger.Infof("Sent disconnect notification to worker %s", worker.ID)
		}
		
		// ワーカーの状態を切断処理中に変更
		worker.mu.Lock()
		worker.State = StateDisconnecting
		worker.mu.Unlock()
		
		// @obj: 各ワーカーに対して切断スレッドを起動
		// @ref: SCIK9X27-000003-000029, SCIK9X27-000003-000030
		tm.wg.Add(1)
		go tm.workerDisconnectThread(worker.ID)
	}
}

// waitForWorkersDisconnect はワーカーの切断を待つ
// @obj: 指定時間内にワーカーが切断されるのを待機
// @ref: SCIK9X27-000003-00001E - ワーカーの切断待ち
func (tm *TaskMaster) waitForWorkersDisconnect(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.workersMu.RLock()
			workerCount := len(tm.workers)
			tm.workersMu.RUnlock()

			if workerCount == 0 {
				tm.logger.Info("All workers disconnected")
				return
			}

			if time.Now().After(deadline) {
				tm.logger.Warn("Timeout waiting for workers to disconnect")
				// 強制的に接続を閉じる
				tm.forceDisconnectAllWorkers()
				return
			}
		}
	}
}

// forceDisconnectAllWorkers は全ワーカーを強制切断する
// @obj: タイムアウト時に残っているワーカーを強制的に切断
// @ref: SCIK9X27-000003-00001E - 強制切断処理
func (tm *TaskMaster) forceDisconnectAllWorkers() {
	tm.workersMu.Lock()
	defer tm.workersMu.Unlock()

	for workerID, worker := range tm.workers {
		tm.logger.Warnf("Force disconnecting worker %s", workerID)
		worker.Conn.Close()
		
		// 実行中のタスクをpendingに戻す
		if worker.CurrentTask != "" && worker.State == StateWorking {
			tm.returnTaskToPending(worker.CurrentTask)
		}
	}
	
	// ワーカーマップをクリア
	tm.workers = make(map[string]*WorkerInfo)
}

// displayStatistics は統計情報を表示する
// @obj: シャットダウン時に合計稼働時間、全タスク数、成功/失敗数を表示
// @ref: SCIK9X27-000003-000034
func (tm *TaskMaster) displayStatistics() {
	duration, totalTasks, successTasks, failedTasks := tm.getStatistics()
	
	// 時間を見やすい形式に変換
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60
	
	// 成功率と失敗率の計算
	var successRate, failureRate float64
	if totalTasks > 0 {
		successRate = float64(successTasks) / float64(totalTasks) * 100
		failureRate = float64(failedTasks) / float64(totalTasks) * 100
	}
	
	tm.logger.Info("=== タスク管理マスター統計情報 ===")
	tm.logger.Infof("合計稼働時間: %d時間%d分%d秒", hours, minutes, seconds)
	tm.logger.Infof("全タスク数: %d", totalTasks)
	tm.logger.Infof("成功タスク数: %d (%.1f%%)", successTasks, successRate)
	tm.logger.Infof("失敗タスク数: %d (%.1f%%)", failedTasks, failureRate)
	tm.logger.Info("==============================")
}

// workerDisconnectThread は個別ワーカーの切断処理を行うスレッド
// @obj: 5秒タイムアウトでワーカーの切断を監視し、完了時にDISCONNECT内部メッセージを送信
// @ref: SCIK9X27-000003-000029, SCIK9X27-000003-000030
func (tm *TaskMaster) workerDisconnectThread(workerID string) {
	defer tm.wg.Done()
	
	tm.logger.Debugf("Starting disconnect thread for worker %s", workerID)
	
	// @obj: 5秒タイムアウトでワーカーの切断を待機
	// @ref: SCIK9X27-000003-000030
	timeout := 5 * time.Second
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	deadline := time.Now().Add(timeout)
	
	for {
		select {
		case <-tm.ctx.Done():
			// マスター全体の終了
			return
			
		case <-ticker.C:
			// ワーカーがまだ存在するかチェック
			tm.workersMu.RLock()
			_, exists := tm.workers[workerID]
			tm.workersMu.RUnlock()
			
			if !exists {
				// ワーカーが正常に切断完了
				tm.logger.Debugf("Worker %s disconnected normally", workerID)
				
				// @obj: DISCONNECT内部メッセージをメインループに送信
				// @ref: SCIK9X27-000003-000030
				disconnectMsg := &InternalMessage{
					Message:  common.NewMessage(common.TypeDisconnect, "Worker disconnected", ""),
					WorkerID: workerID,
				}
				
				select {
				case tm.msgChan <- disconnectMsg:
					tm.logger.Debugf("Sent DISCONNECT message for worker %s", workerID)
				case <-time.After(1 * time.Second):
					tm.logger.Warnf("Timeout sending DISCONNECT message for worker %s", workerID)
				}
				return
			}
			
			if time.Now().After(deadline) {
				// タイムアウト：強制切断
				tm.logger.Warnf("Worker %s disconnect timeout, forcing disconnection", workerID)
				
				tm.workersMu.Lock()
				if worker, exists := tm.workers[workerID]; exists {
					worker.Conn.Close()
					// 実行中のタスクをpendingに戻す
					if worker.CurrentTask != "" && worker.State == StateWorking {
						tm.returnTaskToPending(worker.CurrentTask)
					}
					delete(tm.workers, workerID)
				}
				tm.workersMu.Unlock()
				
				// DISCONNECT内部メッセージを送信
				disconnectMsg := &InternalMessage{
					Message:  common.NewMessage(common.TypeDisconnect, "Worker force disconnected", ""),
					WorkerID: workerID,
				}
				
				select {
				case tm.msgChan <- disconnectMsg:
					tm.logger.Debugf("Sent DISCONNECT message for force-disconnected worker %s", workerID)
				case <-time.After(1 * time.Second):
					tm.logger.Warnf("Timeout sending DISCONNECT message for force-disconnected worker %s", workerID)
				}
				return
			}
		}
	}
}