package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

/*
@obj: マスターからメッセージを受信する
@ref: IPS3MKEQ-000000-000013 "メインループでは、複数のメッセージをまとめて受信する場合があることを考慮すること。"
*/
func (w *Worker) receiveFromMaster() {
	defer w.wg.Done()
	
	reader := bufio.NewReader(w.conn)
	for {
		/*
		@obj: メッセージを1行ずつ読み込む
		@ref: IPS3MKEQ-000004-000013 "タスク管理マスタとタスクワーカー間のTCP通信では、各JSONメッセージの末尾に改行文字（\n）を付与して送信する。"
		*/
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("Failed to read from master: %v", err)
			}
			// 接続が切れた場合は終了
			w.cancel()
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		msg, err := common.UnmarshalMessage([]byte(line))
		if err != nil {
			log.Printf("Failed to unmarshal message: %v", err)
			continue
		}

		select {
		case w.masterMsgChan <- msg:
		case <-w.ctx.Done():
			return
		}
	}
}

/*
@obj: ヘルスチェックメッセージを処理する
@ref: IPS3MKEQ-000000-000014 "ヘルスチェックメッセージ（type=CHECK）を受け取ると、即時にtype=CHECK_ACKを返答する。返答メッセージには、受信したリクエストIDを含める。"
*/
func (w *Worker) handleHealthCheck(msg *common.Message) {
	/*
	@obj: CHECK_ACKメッセージを返信する
	@ref: IPS3MKEQ-000000-000031 'CHECK_ACK: { "type": "CHECK_ACK", "msg": "", "req_id": "<リクエストID>" }'
	*/
	response := &common.Message{
		Type:  common.MessageTypeCheckAck,
		Msg:   "",
		ReqID: msg.ReqID,
	}
	
	if err := w.sendMessage(response); err != nil {
		log.Printf("Failed to send CHECK_ACK: %v", err)
	}
}

/*
@obj: タスク実行依頼を処理する
@ref: IPS3MKEQ-000000-000015 "タスク実行依頼メッセージ（type=REQUEST）を受け取ると、まず即時にタスク実行依頼承諾メッセージ（type=REQUEST_ACK）を返答し、その後、受け取ったタスク実行依頼メッセージをclaude管理コルーチンに渡す。"
*/
func (w *Worker) handleTaskRequest(msg *common.Message) {
	/*
	@obj: REQUEST_ACKメッセージを即座に返信する
	@ref: IPS3MKEQ-000000-000015 "まず即時にタスク実行依頼承諾メッセージ（type=REQUEST_ACK）を返答し"
	@ref: IPS3MKEQ-000000-000016 "タスク実行依頼承諾メッセージには、受信したリクエストIDを含める。"
	@ref: IPS3MKEQ-000000-000030 'REQUEST_ACK: { "type": "REQUEST_ACK", "msg": "", "req_id": "<リクエストID>" }'
	*/
	response := &common.Message{
		Type:  common.MessageTypeRequestAck,
		Msg:   "",
		ReqID: msg.ReqID,
	}
	
	if err := w.sendMessage(response); err != nil {
		log.Printf("Failed to send REQUEST_ACK: %v", err)
		return
	}

	/*
	@obj: タスク実行依頼をClaude管理コルーチンに渡す
	@ref: IPS3MKEQ-000000-000015 "その後、受け取ったタスク実行依頼メッセージをclaude管理コルーチンに渡す。"
	*/
	w.claudeMsgChan <- ClaudeMessage{
		Type: common.MessageTypeRequest,
		Msg:  msg.Msg,
	}
}

/*
@obj: Claude管理コルーチンからの結果を処理する
@ref: IPS3MKEQ-000000-000016 "claude管理コルーチンからのタスク完了通知は、そのままタスク管理マスタにタスク結果報告を送信する。"
*/
func (w *Worker) handleClaudeResult(msg ClaudeMessage) {
	log.Printf("Handling Claude result: %s", msg.Type)
	var response *common.Message
	
	switch msg.Type {
	case common.MessageTypeDone:
		/*
		@obj: 成功メッセージを送信する
		@ref: IPS3MKEQ-000000-000017 "タスク結果報告メッセージには、type=DONE、FAILED、USAGE_LIMITEDのいずれかと、msgフィールドにタスクファイル名を記載する。"
		@ref: IPS3MKEQ-000000-000032 'DONE: { "type": "DONE", "msg": "<タスクファイル名>" }'
		*/
		response = &common.Message{
			Type: common.MessageTypeDone,
			Msg:  msg.Msg,
		}
	case common.MessageTypeFailed:
		/*
		@obj: 失敗メッセージを送信する
		@ref: IPS3MKEQ-000000-000033 'FAILED: { "type": "FAILED", "msg": "<タスクファイル名>" }'
		*/
		response = &common.Message{
			Type: common.MessageTypeFailed,
			Msg:  msg.Msg,
		}
	case common.MessageTypeUsageLimited:
		/*
		@obj: レートリミットメッセージを送信する
		@ref: IPS3MKEQ-000000-000035 'USAGE_LIMITED: {"type": "USAGE_LIMITED", "msg": "<ワーカーID>" }'
		*/
		response = &common.Message{
			Type: common.MessageTypeUsageLimited,
			Msg:  w.workerID,
		}
	}

	if response != nil {
		log.Printf("Sending response to master: %+v", response)
		if err := w.sendMessage(response); err != nil {
			log.Printf("Failed to send task result: %v", err)
		} else {
			log.Printf("Successfully sent task result to master")
		}
	}
}

/*
@obj: Claude管理コルーチンを実行する
@ref: IPS3MKEQ-000000-00001D "claude管理コルーチンは、claudeコマンドを実行して、その完了を待つ役割を担う。"
*/
func (w *Worker) claudeManagerCoroutine() {
	defer w.wg.Done()

	/*
	@obj: メッセージ待ち受けループ
	@ref: IPS3MKEQ-000000-00001F "メッセージ待ち受けループは、以下のメッセージやclaudeからの出力を待ち受け、処理する。"
	@ref: IPS3MKEQ-000000-000024 "メッセージ待ち受けループでは、複数のメッセージをまとめて受信する場合があることを考慮すること。"
	*/
	for {
		select {
		case <-w.ctx.Done():
			/*
			@obj: 終了処理
			@ref: IPS3MKEQ-000000-00002D "待ち受けループが終了メッセージを受け取ったら、待ち受けループから脱してコルーチンを終了する。"
			*/
			return

		case msg := <-w.claudeMsgChan:
			log.Printf("Claude manager received message: %s", msg.Type)
			switch msg.Type {
			case common.MessageTypeRequest:
				/*
				@obj: タスク実行依頼を処理する
				@ref: IPS3MKEQ-000000-000020 "メインループからのタスク実行依頼（REQUEST）"
				@ref: IPS3MKEQ-000000-000025 "タスク実行依頼を受け取ると、そこに書かれているプロンプトテキストをAIエージェントに与え、タスク実行を開始する。"
				*/
				w.executeTask(msg.Msg)
			case common.MessageTypeExit:
				/*
				@obj: 終了メッセージを処理する
				@ref: IPS3MKEQ-000000-000021 "メインループからの終了メッセージ（EXIT）"
				*/
				return
			}
		}
	}
}

/*
@obj: タスクを実行する
@ref: IPS3MKEQ-000000-000044 "claudeコマンドの実行は、`claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash -p <プロンプト文字列>`のように実行する。"
@ref: IPS3MKEQ-000000-00001E "claudeコマンドの実行は、`claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash -p <プロンプト文字列>`のように実行する。"
*/
func (w *Worker) executeTask(promptText string) {
	log.Printf("Executing task with prompt length: %d", len(promptText))
	/*
	@obj: Claudeコマンドを準備する
	@ref: IPS3MKEQ-000000-000044 "claudeコマンドの実行は、`claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash -p <プロンプト文字列>`のように実行する。"
	@ref: IPS3MKEQ-000006-000015 "取得したセッションIDでタスクを実行"
	*/
	/*
	@obj: Claudeコマンドの引数を構築する
	@ref: IPS3MKEQ-000000-000006 'このツールのオプショナルの引数に--opusが指定されていた場合: `claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash --model opus`'
	@ref: IPS3MKEQ-000000-000007 'このツールのオプショナルの引数指定がない場合: `claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash --model sonnet`'
	*/
	var cmdArgs []string
	if w.config.Opus {
		cmdArgs = []string{"claude", "-r", w.sessionID, "--allowedTools", "WebFetch,Read,Write,Bash", "--model", "opus", "--output-format", "json", "-p", "-"}
	} else {
		cmdArgs = []string{"claude", "-r", w.sessionID, "--allowedTools", "WebFetch,Read,Write,Bash", "--model", "sonnet", "--output-format", "json", "-p", "-"}
	}

	/*
	@obj: タイムアウト付きコンテキストを作成する
	@ref: IPS3MKEQ-000000-000039 "タスク管理マスタは、REQUESTおよびCHECKメッセージに対して3秒のタイムアウトを設定している。"
	*/
	ctx, cancel := context.WithTimeout(w.ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = w.config.RootDir
	
	/*
	@obj: プロンプトテキストを標準入力として設定する
	@ref: IPS3MKEQ-000000-00001E "claudeコマンドの実行は、`claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash -p <プロンプト文字列>`のように実行する。"
	*/
	cmd.Stdin = strings.NewReader(promptText)

	/*
	@obj: Claudeの出力を処理する
	@ref: IPS3MKEQ-000000-000022 "claudeからの出力"
	*/
	log.Printf("Executing claude command with prompt...")
	output, err := cmd.CombinedOutput()
	
	log.Printf("Claude command completed with error: %v", err)
	log.Printf("Output length: %d bytes", len(output))
	
	if err != nil {
		log.Printf("Claude command failed: %v", err)
		log.Printf("Output: %s", string(output))
		/*
		@obj: エラーを判定する
		@ref: IPS3MKEQ-000000-000029 "usage limitになった場合はUSAGE_LIMITEDを送る"
		@ref: IPS3MKEQ-000000-00002A "タスク実行が何らかの理由で失敗した場合はFAILEDを送る"
		*/
		if strings.Contains(err.Error(), "usage") || strings.Contains(err.Error(), "limit") {
			w.sendTaskResult(common.MessageTypeUsageLimited, w.workerID)
		} else {
			w.sendTaskResult(common.MessageTypeFailed, "")
		}
		return
	}

	/*
	@obj: Claudeの出力を処理する
	@ref: IPS3MKEQ-000000-000026 "ログ出力には、AIエージェントの出力をそのまま出すのではなく、JSONオブジェクト内の、contentの中だけを出力する。"
	*/
	if len(output) > 0 {
		var result struct {
			Type      string `json:"type"`
			Result    string `json:"result"`
			IsError   bool   `json:"is_error"`
			ErrorMsg  string `json:"error_message"`
		}
		if err := json.Unmarshal(output, &result); err == nil {
			if result.IsError {
				log.Printf("Claude returned error: %s", result.ErrorMsg)
				w.sendTaskResult(common.MessageTypeFailed, "")
				return
			}
			log.Printf("Claude result: %s", result.Result)
		} else {
			log.Printf("Failed to parse Claude output as JSON: %v", err)
		}
	}

	/*
	@obj: タスク成功を報告する
	@ref: IPS3MKEQ-000000-000028 "タスク実行が成功した場合はDONEを送る"
	*/
	log.Printf("Claude task completed successfully")
	w.sendTaskResult(common.MessageTypeDone, "")

	/*
	@obj: コンテキストをクリアする
	@ref: IPS3MKEQ-000000-00002C "また、タスク実行完了通知送信後に、必ずAIエージェントに対して、\"/clear\"コマンドを実行し、コンテキストを消去する。"
	*/
	w.clearContext()
}


/*
@obj: コンテキストをクリアする
@ref: IPS3MKEQ-000000-00002C "また、タスク実行完了通知送信後に、必ずAIエージェントに対して、\"/clear\"コマンドを実行し、コンテキストを消去する。このコマンドもcontinue_conversationオプションをTrueにして実行する。"
*/
func (w *Worker) clearContext() {
	/*
	@obj: /clearコマンドを実行してコンテキストをクリアする
	@ref: IPS3MKEQ-000000-00002C "必ずAIエージェントに対して、\"/clear\"コマンドを実行し、コンテキストを消去する。"
	*/
	cmdArgs := []string{"claude", "-r", w.sessionID, "--output-format", "json", "-p", "/clear"}
	
	ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = w.config.RootDir
	
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("Failed to clear context: %v (output: %s)", err, string(output))
	} else {
		log.Printf("Context cleared successfully")
	}
}

/*
@obj: タスク結果をClaude管理コルーチンに送信する
@ref: IPS3MKEQ-000000-000028 "claude管理コルーチンは、AIエージェントのタスクが完了したら、メインループに対して以下のいずれかのタスク実行完了通知を送る。"
*/
func (w *Worker) sendTaskResult(resultType, msg string) {
	log.Printf("Sending task result: %s", resultType)
	w.claudeMsgChan <- ClaudeMessage{
		Type: resultType,
		Msg:  msg,
	}
}

/*
@obj: メッセージをマスターに送信する
@ref: IPS3MKEQ-000000-00002E "タスクワーカーがタスク管理マスタと送受信するメッセージは、メッセージフォーマット.mdに準拠したJSON形式とする。"
@ref: IPS3MKEQ-000004-000013 "タスク管理マスタとタスクワーカー間のTCP通信では、各JSONメッセージの末尾に改行文字（\n）を付与して送信する。"
*/
func (w *Worker) sendMessage(msg *common.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.conn.Write(append(data, '\n'))
	return err
}

/*
@obj: ワーカーの終了処理を実行する
@ref: IPS3MKEQ-000000-00001A "ワーカー終了処理では、タスク管理マスタとのコネクションを切断してメインループを終了し、claude管理コルーチンを終了させる。その後、プログラムを終了する。"
*/
func (w *Worker) shutdown() error {
	log.Println("Shutting down worker...")

	/*
	@obj: 離脱メッセージを送信する
	@ref: IPS3MKEQ-000000-000034 'LEAVE: { "type": "LEAVE", "msg": ""'
	*/
	leaveMsg := &common.Message{
		Type: common.MessageTypeLeave,
		Msg:  "",
	}
	w.sendMessage(leaveMsg)

	/*
	@obj: Claude管理コルーチンに終了を通知する
	@ref: IPS3MKEQ-000000-00001A "claude管理コルーチンを終了させる"
	*/
	select {
	case w.claudeMsgChan <- ClaudeMessage{Type: common.MessageTypeExit}:
	default:
	}

	/*
	@obj: 全てのゴルーチンの終了を待つ
	@ref: IPS3MKEQ-000000-00001B "ワーカー終了処理中にCtrl-C（SIG_TERM）が発生したら、claude管理コルーチンがあればそれを強制停止して、即座にプログラムを終了する。"
	*/
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Worker shutdown completed")
	case <-w.ctx.Done():
		log.Println("Forced shutdown")
	}

	return nil
}