#!/usr/bin/env python3
"""
タスク管理マスタ

このモジュールは、複数のタスクワーカーとの通信を管理し、
.tasksディレクトリ内のタスクプロンプトファイルの処理を統括する。

主な機能:
- タスクワーカーの接続管理
- タスクの配布と実行管理
- タイムアウト監視
- ヘルスチェック機能
"""

import argparse
import json
import logging
import os
import random
import signal
import socket
import sys
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Tuple


# ログ設定
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)


@dataclass
class RequestInfo:
    """リクエスト情報を管理するクラス"""
    request_id: str
    timeout_time: float


@dataclass
class WorkerObject:
    """ワーカーオブジェクト - 各タスクワーカーの状態を管理"""
    socket: socket.socket
    worker_id: str
    current_task_file: str = ""  # 対応中の.tasks/working/のファイル名
    status: str = "idle"  # idle/requesting/working/disconnecting
    request_infos: List[RequestInfo] = field(default_factory=list)


class TaskMaster:
    """タスク管理マスタのメインクラス"""
    
    def __init__(self, port: int = 34567, root_dir: Optional[str] = None):
        """
        タスク管理マスタを初期化
        
        Args:
            port: 待受ポート番号 (デフォルト: 34567)
            root_dir: ルートディレクトリパス (デフォルト: カレントディレクトリ)
        """
        self.port = port
        self.root_dir = Path(root_dir) if root_dir else Path.cwd()
        self.tasks_dir = self.root_dir / ".tasks"
        
        # ディレクトリ構造の確認・作成
        self._ensure_task_directories()
        
        # 管理用変数
        self.workers: Dict[str, WorkerObject] = {}
        self.server_socket: Optional[socket.socket] = None
        self.running = True
        self.timer_thread: Optional[threading.Thread] = None
        self.message_queue = []
        self.message_lock = threading.Lock()
        
        # 統計情報
        self.start_time = time.time()
        self.total_tasks = 0
        self.success_tasks = 0
        self.failed_tasks = 0
        
        # シグナルハンドラ設定
        signal.signal(signal.SIGINT, self._signal_handler)
        signal.signal(signal.SIGTERM, self._signal_handler)
    
    def _ensure_task_directories(self):
        """必要なタスクディレクトリを作成"""
        for subdir in ["pending", "working", "done", "failed"]:
            dir_path = self.tasks_dir / subdir
            dir_path.mkdir(parents=True, exist_ok=True)
            logger.info(f"ディレクトリ確認: {dir_path}")
    
    def _signal_handler(self, signum, frame):
        """シグナルハンドラ - Ctrl-C等での終了処理"""
        logger.info("終了シグナルを受信しました")
        self.running = False
        self._add_message({"type": "COMPLETED", "msg": ""})
    
    def _add_message(self, message: dict):
        """スレッド間メッセージをキューに追加"""
        with self.message_lock:
            self.message_queue.append(message)
    
    def _get_messages(self) -> List[dict]:
        """キューからメッセージを取得してクリア"""
        with self.message_lock:
            messages = self.message_queue.copy()
            self.message_queue.clear()
            return messages
    
    def start(self):
        """タスク管理マスタを開始"""
        logger.info(f"タスク管理マスタを開始 (ポート: {self.port}, ルート: {self.root_dir})")
        
        # サーバーソケット作成
        self.server_socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.server_socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.server_socket.bind(("0.0.0.0", self.port))
        self.server_socket.listen(10)
        self.server_socket.settimeout(1.0)  # ノンブロッキング処理のため
        
        # タイマースレッド開始
        self.timer_thread = threading.Thread(target=self._timer_thread, daemon=True)
        self.timer_thread.start()
        
        logger.info("メインループを開始")
        self._main_loop()
    
    def _timer_thread(self):
        """タイマースレッド - 定期的にタイマーイベントを送信（デフォルト10秒）"""
        timer_interval = 10  # デフォルト設定は10秒ごと
        while self.running:
            time.sleep(timer_interval)
            if self.running:
                self._add_message({"type": "TIMER", "msg": ""})
    
    def _main_loop(self):
        """メインループ - イベント待ち受けと処理"""
        while self.running:
            try:
                # 新しい接続の受け入れ
                self._accept_new_connections()
                
                # 既存ワーカーからのメッセージ受信
                self._receive_worker_messages()
                
                # スレッド間メッセージの処理
                self._process_internal_messages()
                
                # タスク依頼処理
                self._process_pending_tasks()
                
            except Exception as e:
                logger.error(f"メインループでエラーが発生: {e}")
        
        logger.info("メインループを終了")
        self._shutdown()
    
    def _accept_new_connections(self):
        """新しいタスクワーカーの接続を受け入れ"""
        try:
            client_socket, address = self.server_socket.accept()
            logger.info(f"新しい接続を受け入れ: {address}")
            
            # JOINメッセージの待機
            threading.Thread(
                target=self._handle_new_worker,
                args=(client_socket, address),
                daemon=True
            ).start()
            
        except socket.timeout:
            pass  # タイムアウトは正常な動作
        except Exception as e:
            if self.running:
                logger.error(f"接続受け入れエラー: {e}")
    
    def _handle_new_worker(self, client_socket: socket.socket, address):
        """新しいワーカーの初期処理"""
        try:
            client_socket.settimeout(5.0)
            data = client_socket.recv(1024).decode('utf-8')
            message = json.loads(data)
            
            if message.get("type") == "JOIN":
                worker_id = message.get("msg", f"worker_{address[0]}_{address[1]}")
                
                # ワーカーオブジェクト作成
                worker = WorkerObject(
                    socket=client_socket,
                    worker_id=worker_id,
                    status="idle"
                )
                self.workers[worker_id] = worker
                
                # JOIN_ACK応答
                response = {"type": "JOIN_ACK", "msg": ""}
                client_socket.send(json.dumps(response).encode('utf-8'))
                
                logger.info(f"ワーカー {worker_id} が参入しました")
            else:
                logger.warning(f"無効な参入メッセージ: {message}")
                client_socket.close()
                
        except Exception as e:
            logger.error(f"新しいワーカー処理エラー: {e}")
            client_socket.close()
    
    def _receive_worker_messages(self):
        """既存ワーカーからのメッセージ受信"""
        disconnected_workers = []
        
        for worker_id, worker in self.workers.items():
            try:
                worker.socket.settimeout(0.1)
                data = worker.socket.recv(4096).decode('utf-8', errors='replace')
                if data:
                    # 複数のJSONメッセージが連結されている可能性を考慮
                    messages = self._parse_json_messages(data)
                    for message in messages:
                        if message:
                            self._handle_worker_message(worker_id, message)
                else:
                    # 接続切断
                    disconnected_workers.append(worker_id)
                    
            except socket.timeout:
                pass  # タイムアウトは正常
            except Exception as e:
                logger.error(f"ワーカー {worker_id} からのメッセージ受信エラー: {e}")
                logger.debug(f"受信データ: {repr(data) if 'data' in locals() else 'N/A'}")
                disconnected_workers.append(worker_id)
        
        # 切断されたワーカーの処理
        for worker_id in disconnected_workers:
            self._handle_worker_disconnect(worker_id)
    
    def _handle_worker_message(self, worker_id: str, message: dict):
        """ワーカーからのメッセージを処理"""
        msg_type = message.get("type")
        worker = self.workers.get(worker_id)
        
        if not worker:
            return
        
        if msg_type == "REQUEST_ACK":
            # タスク実行依頼承諾
            req_id = message.get("req_id")
            if req_id:
                # 稼働状態をworkingに変更、リクエスト情報を削除
                worker.status = "working"
                worker.request_infos = [r for r in worker.request_infos if r.request_id != req_id]
                logger.info(f"ワーカー {worker_id} がタスク実行を開始 (req_id: {req_id})")
        
        elif msg_type in ["DONE", "FAILED"]:
            # タスク完了報告
            self._handle_task_completion(worker_id, msg_type, message.get("msg", ""))
        
        elif msg_type == "CHECK_ACK":
            # ヘルスチェック応答
            req_id = message.get("req_id")
            if req_id:
                worker.request_infos = [r for r in worker.request_infos if r.request_id != req_id]
                logger.debug(f"ワーカー {worker_id} ヘルスチェック正常 (req_id: {req_id})")
        
        elif msg_type == "LEAVE":
            # 離脱通知
            self._handle_worker_disconnect(worker_id)
    
    def _handle_task_completion(self, worker_id: str, result_type: str, task_file: str):
        """タスク完了処理"""
        worker = self.workers.get(worker_id)
        if not worker:
            return
        
        # .tasks/working/から適切なディレクトリに移動
        working_file = self.tasks_dir / "working" / worker.current_task_file
        
        if result_type == "DONE":
            target_dir = self.tasks_dir / "done"
            self.success_tasks += 1
            logger.info(f"タスク完了: {worker.current_task_file} (ワーカー: {worker_id})")
        else:  # FAILED
            target_dir = self.tasks_dir / "failed"
            self.failed_tasks += 1
            logger.info(f"タスク失敗: {worker.current_task_file} (ワーカー: {worker_id})")
        
        # ファイル移動
        try:
            if working_file.exists():
                target_path = target_dir / worker.current_task_file
                working_file.rename(target_path)
                logger.debug(f"ファイル移動完了: {worker.current_task_file} → {target_dir.name}")
        except Exception as e:
            logger.error(f"タスクファイル移動エラー: {e}")
        
        # ワーカー状態をidleに戻し、タスクファイル情報をクリア
        worker.status = "idle"
        worker.current_task_file = ""
    
    def _process_internal_messages(self):
        """スレッド間メッセージの処理"""
        messages = self._get_messages()
        
        for message in messages:
            msg_type = message.get("type")
            
            if msg_type == "TIMER":
                self._handle_timer_event()
            
            elif msg_type == "COMPLETED":
                self.running = False
                break
            
            elif msg_type == "DISCONNECT":
                worker_id = message.get("msg")
                if worker_id and worker_id in self.workers:
                    del self.workers[worker_id]
                    logger.info(f"ワーカー {worker_id} を削除しました")
            
            elif msg_type == "TIMEOUT_CHECK":
                req_id = message.get("msg")
                self._handle_timeout_check(req_id)
    
    def _handle_timer_event(self):
        """タイマーイベント処理"""
        # ファイル数チェック
        pending_count = len(list((self.tasks_dir / "pending").glob("*")))
        working_count = len(list((self.tasks_dir / "working").glob("*")))
        
        if pending_count + working_count == 0:
            logger.info("全タスク完了を検出")
            self._add_message({"type": "COMPLETED", "msg": ""})
            return
        
        # ワーカーヘルスチェック
        self._perform_health_checks()
    
    def _perform_health_checks(self):
        """全ワーカーのヘルスチェック実行"""
        for worker_id, worker in self.workers.items():
            try:
                req_id = f"check_{random.randint(1000, 9999)}"
                check_message = {
                    "type": "CHECK",
                    "msg": "",
                    "req_id": req_id
                }
                
                worker.socket.send(json.dumps(check_message).encode('utf-8'))
                
                # リクエスト情報追加
                request_info = RequestInfo(
                    request_id=req_id,
                    timeout_time=time.time() + 3
                )
                worker.request_infos.append(request_info)
                
                # タイムアウト監視スレッド起動
                threading.Thread(
                    target=self._timeout_monitor,
                    args=(req_id, 3),
                    daemon=True
                ).start()
                
            except Exception as e:
                logger.error(f"ワーカー {worker_id} ヘルスチェック送信失敗: {e}")
                self._handle_worker_disconnect(worker_id)
    
    def _process_pending_tasks(self):
        """保留中タスクの処理"""
        pending_dir = self.tasks_dir / "pending"
        working_dir = self.tasks_dir / "working"
        
        # 保留中タスクファイルを取得
        pending_files = list(pending_dir.glob("*"))
        if not pending_files:
            return
        
        # アイドル状態のワーカーを検索
        idle_workers = [w for w in self.workers.values() if w.status == "idle"]
        if not idle_workers:
            return
        
        # タスクを割り当て
        for i, task_file in enumerate(pending_files):
            if i >= len(idle_workers):
                break
            
            worker = idle_workers[i]
            
            try:
                # ファイルを working ディレクトリに移動
                working_file = working_dir / task_file.name
                task_file.rename(working_file)
                
                # プロンプトテキスト読み込み
                prompt_text = working_file.read_text(encoding='utf-8')
                
                # リクエストID生成
                req_id = f"task_{random.randint(1000, 9999)}"
                
                # REQUESTメッセージ送信
                request_message = {
                    "type": "REQUEST",
                    "msg": prompt_text,
                    "req_id": req_id
                }
                
                # JSON送信時にエスケープ問題を回避
                json_data = json.dumps(request_message, ensure_ascii=False)
                worker.socket.send(json_data.encode('utf-8'))
                
                # ワーカー状態更新
                worker.status = "requesting"
                worker.current_task_file = working_file.name
                request_info = RequestInfo(
                    request_id=req_id,
                    timeout_time=time.time() + 3
                )
                worker.request_infos.append(request_info)
                
                # タイムアウト監視スレッド起動
                threading.Thread(
                    target=self._timeout_monitor,
                    args=(req_id, 3),
                    daemon=True
                ).start()
                
                self.total_tasks += 1
                logger.info(f"タスク割り当て: {task_file.name} → ワーカー {worker.worker_id}")
                
            except Exception as e:
                logger.error(f"タスク割り当てエラー: {e}")
                # ファイルを pending に戻す
                if working_file.exists():
                    working_file.rename(task_file)
    
    def _timeout_monitor(self, req_id: str, timeout_seconds: int):
        """タイムアウト監視スレッド"""
        time.sleep(timeout_seconds)
        self._add_message({"type": "TIMEOUT_CHECK", "msg": req_id})
    
    def _handle_timeout_check(self, req_id: str):
        """タイムアウト確認処理"""
        current_time = time.time()
        
        for worker_id, worker in self.workers.items():
            # 該当するリクエスト情報を検索
            for request_info in worker.request_infos:
                if request_info.request_id == req_id:
                    # タイムアウト判定
                    if current_time > request_info.timeout_time:
                        logger.warning(f"ワーカー {worker_id} タイムアウト (req_id: {req_id})")
                        # リクエスト情報削除
                        worker.request_infos.remove(request_info)
                        # ワーカー切断処理
                        self._handle_worker_disconnect(worker_id)
                    return
    
    def _handle_worker_disconnect(self, worker_id: str):
        """ワーカー切断処理"""
        worker = self.workers.get(worker_id)
        if not worker:
            return
        
        # workingの場合、実施中のプロンプトファイルをpendingに移動
        if worker.status == "working" and worker.current_task_file:
            working_file = self.tasks_dir / "working" / worker.current_task_file
            pending_file = self.tasks_dir / "pending" / worker.current_task_file
            
            try:
                if working_file.exists():
                    working_file.rename(pending_file)
                    logger.info(f"実施中タスクをpendingに移動: {worker.current_task_file}")
            except Exception as e:
                logger.error(f"タスクファイル移動エラー: {e}")
        
        worker.status = "exiting"
        
        # ワーカー切断スレッド起動
        threading.Thread(
            target=self._disconnect_worker,
            args=(worker_id,),
            daemon=True
        ).start()
    
    def _disconnect_worker(self, worker_id: str):
        """ワーカー切断スレッド"""
        worker = self.workers.get(worker_id)
        if not worker:
            return
        
        try:
            # 5秒のタイムアウトで切断
            worker.socket.settimeout(5.0)
            worker.socket.close()
        except Exception as e:
            logger.error(f"ワーカー {worker_id} 切断エラー: {e}")
        finally:
            # 切断完了通知
            self._add_message({"type": "DISCONNECT", "msg": worker_id})
            logger.info(f"ワーカー {worker_id} 切断完了")
    
    def _parse_json_messages(self, data: str) -> List[dict]:
        """受信データから複数のJSONメッセージを解析"""
        messages = []
        
        # 改行で区切って各行を個別に処理
        lines = data.strip().split('\n')
        
        for line in lines:
            line = line.strip()
            if not line:
                continue
                
            try:
                # JSONとして解析を試行
                message = json.loads(line)
                messages.append(message)
            except json.JSONDecodeError as e:
                logger.warning(f"JSON解析失敗: {e}")
                logger.debug(f"問題のある行: {repr(line)}")
                
                # 複数のJSONが{}{}のように連結されている場合の処理
                try:
                    # 単純な分割処理を試行
                    json_parts = self._split_concatenated_json(line)
                    for part in json_parts:
                        if part.strip():
                            try:
                                message = json.loads(part.strip())
                                messages.append(message)
                            except json.JSONDecodeError:
                                logger.warning(f"分割後もJSON解析失敗: {repr(part)}")
                except Exception as split_error:
                    logger.error(f"JSON分割処理エラー: {split_error}")
        
        return messages
    
    def _split_concatenated_json(self, data: str) -> List[str]:
        """連結されたJSONを分割"""
        json_objects = []
        brace_count = 0
        current_json = ""
        
        for char in data:
            current_json += char
            
            if char == '{':
                brace_count += 1
            elif char == '}':
                brace_count -= 1
                
                # 完全なJSONオブジェクトが完成
                if brace_count == 0:
                    json_objects.append(current_json)
                    current_json = ""
        
        # 残りのデータがあれば追加
        if current_json.strip():
            json_objects.append(current_json)
        
        return json_objects

    def _shutdown(self):
        """タスク管理マスタ終了処理"""
        logger.info("タスク管理マスタ終了処理を開始")
        
        # 統計情報表示
        elapsed_time = time.time() - self.start_time
        logger.info(f"合計稼働時間: {elapsed_time:.2f}秒")
        logger.info(f"全タスク数: {self.total_tasks}")
        logger.info(f"成功タスク数: {self.success_tasks}")
        logger.info(f"失敗タスク数: {self.failed_tasks}")
        
        # 全コネクション切断
        disconnect_threads = []
        for worker_id in list(self.workers.keys()):
            thread = threading.Thread(
                target=self._disconnect_worker,
                args=(worker_id,),
                daemon=True
            )
            thread.start()
            disconnect_threads.append(thread)
        
        # 10秒のタイムアウトで切断完了を待機
        start_time = time.time()
        while disconnect_threads and (time.time() - start_time) < 10:
            disconnect_threads = [t for t in disconnect_threads if t.is_alive()]
            time.sleep(0.1)
        
        if disconnect_threads:
            logger.warning("一部のワーカー切断がタイムアウトしました")
        
        # サーバーソケット切断
        if self.server_socket:
            self.server_socket.close()
        
        logger.info("タスク管理マスタを終了しました")


def main():
    """メイン関数"""
    parser = argparse.ArgumentParser(description='タスク管理マスタ')
    parser.add_argument(
        'port',
        type=int,
        nargs='?',
        default=34567,
        help='待受ポート番号 (デフォルト: 34567)'
    )
    parser.add_argument(
        '--root-dir',
        type=str,
        help='ルートディレクトリパス (デフォルト: カレントディレクトリ)'
    )
    
    args = parser.parse_args()
    
    try:
        task_master = TaskMaster(port=args.port, root_dir=args.root_dir)
        task_master.start()
    except KeyboardInterrupt:
        logger.info("Ctrl-Cで終了")
    except Exception as e:
        logger.error(f"予期しないエラー: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()