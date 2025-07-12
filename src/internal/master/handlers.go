package master

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

/*
@obj: TCP接続を受け入れる
@ref: IPS3MKEQ-000002-000008 "タスクワーカーからのTCP接続受け入れ（bindするIPは0.0.0.0とする）"
*/
func (m *Master) acceptConnections() {
	defer m.wg.Done()
	
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			select {
			case <-m.ctx.Done():
				return
			default:
				log.Printf("Failed to accept connection: %v", err)
				continue
			}
		}

		/*
		@obj: 新しいワーカー接続を処理する
		@ref: IPS3MKEQ-000002-00003D "TCP_NODELAYを設定して即座にメッセージを送信"
		*/
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			tcpConn.SetNoDelay(true)
			/*
			@obj: ソケット受信バッファを大きく設定する
			@ref: IPS3MKEQ-000002-00003C "ソケット受信バッファを大きく設定（65536バイト）"
			*/
			tcpConn.SetReadBuffer(65536)
			tcpConn.SetWriteBuffer(65536)
		}

		go m.handleConnection(conn)
	}
}

/*
@obj: ワーカーからの接続を処理する
@ref: IPS3MKEQ-000002-00000D "メインループでは、複数のメッセージをまとめて受信する場合があることを考慮すること。"
*/
func (m *Master) handleConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)
	workerID := ""

	for {
		/*
		@obj: メッセージを1行ずつ読み込む
		@ref: IPS3MKEQ-000004-000013 "タスク管理マスタとタスクワーカー間のTCP通信では、各JSONメッセージの末尾に改行文字（\n）を付与して送信する。"
		*/
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("Failed to read from worker %s: %v", workerID, err)
			}
			/*
			@obj: 接続が切れた場合の処理
			@ref: IPS3MKEQ-000002-000027 "メインループがタスクワーカーから離脱メッセージ（LEAVE）を受信した場合、またはタスクワーカーとのTCP接続が切れた場合、該当するワーカーオブジェクトに対してワーカー切断処理を実施する。"
			*/
			if workerID != "" {
				m.msgChan <- WorkerMessage{
					WorkerID: workerID,
					Message: &common.Message{
						Type: common.MessageTypeLeave,
					},
				}
				// connMapからも削除
				m.connMu.Lock()
				delete(m.connMap, workerID)
				m.connMu.Unlock()
			}
			conn.Close()
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

		/*
		@obj: JOINメッセージの場合はワーカーIDを記録する
		@ref: IPS3MKEQ-000000-00000C "参入メッセージには、type=JOINと、msgフィールドにワーカーIDを含める。"
		*/
		if msg.Type == common.MessageTypeJoin {
			workerID = msg.Msg
			m.connMu.Lock()
			m.connMap[workerID] = conn
			m.connMu.Unlock()
		}

		m.msgChan <- WorkerMessage{
			WorkerID: workerID,
			Message:  msg,
		}
	}
}

/*
@obj: タイマースレッドを実行する
@ref: IPS3MKEQ-000002-00000E "タイマースレッドは、定期的にタイマーイベントメッセージをメインループに送信する。デフォルト設定は10秒ごととする。"
@ref: IPS3MKEQ-000002-00000F "タイマースレッドは、メインループが終了するまで生存する。"
*/
func (m *Master) timerThread() {
	defer m.wg.Done()
	
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.msgChan <- WorkerMessage{
				Message: &common.Message{
					Type: common.MessageTypeTimer,
				},
			}
		}
	}
}

/*
@obj: メッセージを処理する
@ref: IPS3MKEQ-000002-000009 "タスクワーカーからの各種メッセージの受信"
*/
func (m *Master) handleMessage(msg WorkerMessage) {
	switch msg.Message.Type {
	case common.MessageTypeJoin:
		m.handleJoin(msg)
	case common.MessageTypeRequestAck:
		m.handleRequestAck(msg)
	case common.MessageTypeDone, common.MessageTypeFailed, common.MessageTypeUsageLimited:
		m.handleTaskResult(msg)
	case common.MessageTypeCheckAck:
		m.handleCheckAck(msg)
	case common.MessageTypeLeave:
		m.handleLeave(msg)
	case common.MessageTypeTimer:
		m.handleTimer()
	case common.MessageTypeTimeoutCheck:
		m.handleTimeoutCheck(msg)
	case common.MessageTypeDisconnect:
		m.handleDisconnect(msg)
	case common.MessageTypeCompleted:
		m.cancel()
	}
}

/*
@obj: JOINメッセージを処理する
@ref: IPS3MKEQ-000002-000018 "メインループで、タスクワーカーから参入メッセージ（JOIN）を受信したら、当該タスクワーカーのワーカーオブジェクトを生成し、その後、参入応答メッセージを返答する。"
*/
func (m *Master) handleJoin(msg WorkerMessage) {
	m.workersMu.Lock()
	defer m.workersMu.Unlock()

	/*
	@obj: ワーカーオブジェクトを生成する
	@ref: IPS3MKEQ-000002-000019 "ワーカーオブジェクトのタスク稼働状態の初期値は「idle」とする。"
	*/
	m.connMu.RLock()
	conn, exists := m.connMap[msg.WorkerID]
	m.connMu.RUnlock()
	if !exists {
		log.Printf("Connection not found for worker %s", msg.WorkerID)
		return
	}

	worker := &Worker{
		id:       msg.WorkerID,
		conn:     conn,
		state:    StateIdle,
		requests: make([]RequestInfo, 0),
	}
	m.workers[msg.WorkerID] = worker

	/*
	@obj: JOIN_ACKメッセージを送信する
	@ref: IPS3MKEQ-000002-000018 "参入応答メッセージを返答する。なお、参入メッセージには、type=JOINとmsg=ワーカーIDが含まれている。参入応答メッセージにはtype=JOIN_ACKを送る（msgは空文字でよい）。"
	@ref: IPS3MKEQ-000000-000037 'JOIN_ACK: { "type": "JOIN_ACK", "msg": "" }'
	*/
	response := &common.Message{
		Type: common.MessageTypeJoinAck,
		Msg:  "",
	}
	m.sendMessage(worker.conn, response)

	log.Printf("Worker %s joined", msg.WorkerID)
}

/*
@obj: REQUEST_ACKメッセージを処理する
@ref: IPS3MKEQ-000002-000020 "メインループが実行依頼メッセージに対する返答（REQUEST_ACK）を受信すれば、該当するワーカーオブジェクトの稼働状態をworkingに変更し、該当するリクエスト情報を配列から削除する。"
*/
func (m *Master) handleRequestAck(msg WorkerMessage) {
	m.workersMu.Lock()
	defer m.workersMu.Unlock()

	worker, exists := m.workers[msg.WorkerID]
	if !exists {
		return
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	/*
	@obj: 稼働状態をworkingに変更する
	@ref: IPS3MKEQ-000002-000038 "REQUEST_ACK受信時: 稼働状態をworkingに変更、リクエスト情報を配列から削除"
	*/
	worker.state = StateWorking

	/*
	@obj: リクエスト情報を削除する
	@ref: IPS3MKEQ-000002-000020 "該当するリクエスト情報を配列から削除する。（REQUEST_ACKにはリクエストIDが含まれる）"
	*/
	for i := 0; i < len(worker.requests); i++ {
		if worker.requests[i].RequestID == msg.Message.ReqID {
			worker.requests = append(worker.requests[:i], worker.requests[i+1:]...)
			break
		}
	}
}

/*
@obj: タスク結果メッセージを処理する
@ref: IPS3MKEQ-000002-000023 "メインループがタスクワーカーから受け取った結果報告メッセージのtypeがDONEだった場合"
@ref: IPS3MKEQ-000002-000024 "メインループがタスクワーカーから受け取った結果報告メッセージのtypeがFAILEDだった場合"
@ref: IPS3MKEQ-000002-000025 "メインループがタスクワーカーから受け取った結果報告メッセージのtypeがUSAGE_LIMITEDだった場合"
*/
func (m *Master) handleTaskResult(msg WorkerMessage) {
	m.workersMu.Lock()
	defer m.workersMu.Unlock()

	worker, exists := m.workers[msg.WorkerID]
	if !exists {
		return
	}

	worker.mu.Lock()
	workingFile := worker.workingFile
	worker.mu.Unlock()

	if workingFile == "" {
		return
	}

	workingPath := filepath.Join(m.config.RootDir, ".tasks", "working", workingFile)

	switch msg.Message.Type {
	case common.MessageTypeDone:
		/*
		@obj: 成功したタスクをdoneディレクトリに移動する
		@ref: IPS3MKEQ-000002-000023 ".tasks/working/の下の該当タスクプロンプトファイルを.tasks/done/に移動する。"
		*/
		donePath := filepath.Join(m.config.RootDir, ".tasks", "done", workingFile)
		if err := os.Rename(workingPath, donePath); err != nil {
			log.Printf("Failed to move task to done: %v", err)
		}
		m.stats.mu.Lock()
		m.stats.SuccessTasks++
		m.stats.mu.Unlock()

		worker.mu.Lock()
		worker.state = StateIdle
		worker.workingFile = ""
		worker.mu.Unlock()

	case common.MessageTypeFailed:
		/*
		@obj: 失敗したタスクをfailedディレクトリに移動する
		@ref: IPS3MKEQ-000002-000024 ".tasks/failed/に移動する。また、当該タスクワーカーのタスク稼働状態を「idle」に変更する。"
		*/
		failedPath := filepath.Join(m.config.RootDir, ".tasks", "failed", workingFile)
		if err := os.Rename(workingPath, failedPath); err != nil {
			log.Printf("Failed to move task to failed: %v", err)
		}
		m.stats.mu.Lock()
		m.stats.FailedTasks++
		m.stats.mu.Unlock()

		worker.mu.Lock()
		worker.state = StateIdle
		worker.workingFile = ""
		worker.mu.Unlock()

	case common.MessageTypeUsageLimited:
		/*
		@obj: レートリミットの場合はタスクをpendingに戻す
		@ref: IPS3MKEQ-000002-000025 ".tasks/working/の下の当該タスクプロンプトファイルを.tasks/pending/に移動させ、そのワーカーオブジェクトに対してワーカー切断処理を実施する。"
		*/
		pendingPath := filepath.Join(m.config.RootDir, ".tasks", "pending", workingFile)
		if err := os.Rename(workingPath, pendingPath); err != nil {
			log.Printf("Failed to move task back to pending: %v", err)
		}
		m.disconnectWorker(worker)
	}
}

/*
@obj: ペンディングタスクを割り当てる
@ref: IPS3MKEQ-000002-00001A "何らかのイベントが起こるたびに、まだ.tasks/pending/の下にファイルが残っていないかを確認し、残っていれば、以下のタスク依頼処理を実施する。"
@ref: IPS3MKEQ-000002-00001B "タスク依頼処理は、ワーカーオブジェクト群をチェックして、タスク稼働状態が「idle」のものがあった場合に、以下の処理を実施する。"
*/
func (m *Master) assignPendingTasks() {
	pendingDir := filepath.Join(m.config.RootDir, ".tasks", "pending")
	files, err := os.ReadDir(pendingDir)
	if err != nil || len(files) == 0 {
		return
	}

	m.workersMu.RLock()
	defer m.workersMu.RUnlock()

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		/*
		@obj: idleなワーカーを探す
		@ref: IPS3MKEQ-000002-00001B "ワーカーオブジェクト群をチェックして、タスク稼働状態が「idle」のものがあった場合"
		*/
		var idleWorker *Worker
		for _, worker := range m.workers {
			worker.mu.Lock()
			if worker.state == StateIdle {
				idleWorker = worker
				worker.mu.Unlock()
				break
			}
			worker.mu.Unlock()
		}

		if idleWorker == nil {
			break
		}

		/*
		@obj: タスクファイルをworkingディレクトリに移動する
		@ref: IPS3MKEQ-000002-00001C ".tasks/pending/の中から1つファイルを取り出して、.tasks/working/に移動する"
		*/
		pendingPath := filepath.Join(pendingDir, file.Name())
		workingPath := filepath.Join(m.config.RootDir, ".tasks", "working", file.Name())
		if err := os.Rename(pendingPath, workingPath); err != nil {
			log.Printf("Failed to move task to working: %v", err)
			continue
		}

		/*
		@obj: タスクプロンプトを読み込む
		*/
		promptBytes, err := os.ReadFile(workingPath)
		if err != nil {
			log.Printf("Failed to read task prompt: %v", err)
			continue
		}

		/*
		@obj: REQUESTメッセージを送信する
		@ref: IPS3MKEQ-000002-00001D "稼働状態がidleになっているワーカオブジェクトのソケットに対して、今.tasks/working/に移動したタスクプロンプトの実行を依頼するメッセージ（REQUEST）を送る。"
		@ref: IPS3MKEQ-000000-000038 'REQUEST: { "type": "REQUEST", "msg": "<プロンプトテキスト>", "req_id": "<リクエストID>" }'
		*/
		requestID := fmt.Sprintf("req_%d_%d", time.Now().Unix(), rand.Int())
		request := &common.Message{
			Type:  common.MessageTypeRequest,
			Msg:   string(promptBytes),
			ReqID: requestID,
		}

		idleWorker.mu.Lock()
		/*
		@obj: ワーカーの状態を更新する
		@ref: IPS3MKEQ-000002-00001E "そのワーカーオブジェクトの稼働状態をrequestingに、リクエスト情報（タイムアウト時刻を現在時刻＋3秒と、依頼に含めたリクエストID）を追加する。"
		@ref: IPS3MKEQ-000002-000037 "REQUEST送信時: リクエスト情報を配列に追加、タイムアウト監視スレッド起動"
		*/
		idleWorker.state = StateRequesting
		idleWorker.workingFile = file.Name()
		idleWorker.requests = append(idleWorker.requests, RequestInfo{
			RequestID: requestID,
			Timeout:   time.Now().Add(3 * time.Second),
		})
		idleWorker.mu.Unlock()

		if err := m.sendMessage(idleWorker.conn, request); err != nil {
			log.Printf("Failed to send request to worker: %v", err)
			m.disconnectWorker(idleWorker)
			continue
		}

		/*
		@obj: タイムアウト監視スレッドを起動する
		@ref: IPS3MKEQ-000002-00001F "そのワーカーIDとリクエスト情報を引数として、タイムアウト監視スレッドを起動する。"
		*/
		go m.startTimeoutMonitor(idleWorker.id, requestID)

		m.stats.mu.Lock()
		m.stats.TotalTasks++
		m.stats.mu.Unlock()
	}
}

/*
@obj: メッセージを送信する
@ref: IPS3MKEQ-000004-000013 "タスク管理マスタとタスクワーカー間のTCP通信では、各JSONメッセージの末尾に改行文字（\n）を付与して送信する。"
*/
func (m *Master) sendMessage(conn net.Conn, msg *common.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(data, '\n'))
	return err
}