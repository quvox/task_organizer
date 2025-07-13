package master

import (
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

// timerThread は定期的なタイマーイベントを送信するスレッド
// @obj: 5秒間隔でTIMERメッセージをメインループに送信
// @ref: SCIK9X27-000003-00003B
func (tm *TaskMaster) timerThread() {
	defer tm.wg.Done()

	// @obj: デフォルト設定は5秒ごと
	// @ref: SCIK9X27-000003-00003B
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	tm.logger.Debug("Timer thread started")

	for {
		select {
		case <-tm.ctx.Done():
			// @obj: マスター終了時にタイマースレッドも終了
			// @ref: SCIK9X27-000003-00000F
			tm.logger.Debug("Timer thread stopping")
			return

		case <-ticker.C:
			// @obj: TIMERメッセージをメインループに送信
			// @ref: SCIK9X27-000003-00000A
			timerMsg := &InternalMessage{
				Message:  common.NewMessage(common.TypeTimer, "Timer event", ""),
				WorkerID: "TIMER_THREAD",
			}

			select {
			case tm.msgChan <- timerMsg:
				tm.logger.Debug("Sent TIMER message to main loop")
			case <-time.After(1 * time.Second):
				tm.logger.Warn("Timeout sending TIMER message")
			case <-tm.ctx.Done():
				return
			}
		}
	}
}