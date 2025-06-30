#!/usr/bin/env python3
"""
タスクワーカー

このモジュールは、タスク管理マスタに接続し、タスクプロンプトを受信して
claudeコマンドを実行するワーカープロセスです。

主な機能:
- タスク管理マスタとのTCP通信
- ヘルスチェック対応
- タスク実行（claudeコマンド）
- プロセス管理とシグナルハンドリング
"""

import argparse
import json
import logging
import os
import signal
import socket
import subprocess
import sys
import threading
import time
from typing import Optional, Dict, Any
from pathlib import Path


# ログ設定
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)


class TaskWorker:
    """タスクワーカーのメインクラス"""
    
    def __init__(self, host: str = "localhost", port: int = 34567, root_dir: Optional[str] = None, use_opus: bool = False):
        """
        タスクワーカーを初期化
        
        Args:
            host: タスク管理マスタのホスト名 (デフォルト: localhost)
            port: タスク管理マスタのポート番号 (デフォルト: 34567)
            root_dir: ルートディレクトリパス (デフォルト: カレントディレクトリ)
            use_opus: Opus4モデルを使用するかどうか (デフォルト: False、Sonnet4を使用)
        """
        self.host = host
        self.port = port
        self.root_dir = Path(root_dir) if root_dir else Path.cwd()
        self.use_opus = use_opus
        self.socket: Optional[socket.socket] = None
        self.worker_id: str = ""
        self.running = True
        self.claude_process: Optional[subprocess.Popen] = None
        self.claude_thread: Optional[threading.Thread] = None
        self.message_queue = []
        self.message_lock = threading.Lock()
        self.claude_queue = []
        self.claude_lock = threading.Lock()
        
        # シグナルハンドラ設定
        signal.signal(signal.SIGINT, self._signal_handler)
        signal.signal(signal.SIGTERM, self._signal_handler)
    
    def _signal_handler(self, signum, frame):
        """シグナルハンドラ - Ctrl-C等での終了処理"""
        logger.info("終了シグナルを受信しました")
        self.running = False
        self._terminate_claude_process()
        if self.socket:
            try:
                # 離脱メッセージ送信
                leave_message = {"type": "LEAVE", "msg": ""}
                message_data = json.dumps(leave_message) + '\n'
                self.socket.send(message_data.encode('utf-8'))
            except Exception as e:
                logger.error(f"離脱メッセージ送信エラー: {e}")
    
    def _add_message(self, message: dict):
        """スレッド間メッセージをキューに追加"""
        with self.message_lock:
            self.message_queue.append(message)
    
    def _get_messages(self) -> list:
        """キューからメッセージを取得してクリア"""
        with self.message_lock:
            messages = self.message_queue.copy()
            self.message_queue.clear()
            return messages
    
    def _terminate_claude_process(self):
        """現在実行中のclaudeプロセスを強制停止"""
        if self.claude_process and self.claude_process.poll() is None:
            try:
                self.claude_process.terminate()
                # 2秒待って強制終了
                try:
                    self.claude_process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    self.claude_process.kill()
                logger.info("claudeプロセスを停止しました")
            except Exception as e:
                logger.error(f"claudeプロセス停止エラー: {e}")
            finally:
                self.claude_process = None
    
    def start(self):
        """タスクワーカーを開始"""
        logger.info(f"タスクワーカーを開始 (接続先: {self.host}:{self.port})")
        
        try:
            # claude管理スレッドを起動
            self._start_claude_thread()
            
            # タスク管理マスタに接続
            self._connect_to_master()
            
            # 参入処理
            self._join_master()
            
            # 待ち受けループ
            self._main_loop()
            
        except Exception as e:
            logger.error(f"タスクワーカーエラー: {e}")
        finally:
            self._cleanup()
    
    def _connect_to_master(self):
        """タスク管理マスタとのTCP接続を確立"""
        self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.socket.connect((self.host, self.port))
        
        # ワーカーIDを送信元ポート番号から生成
        local_port = self.socket.getsockname()[1]
        self.worker_id = str(local_port)
        
        logger.info(f"タスク管理マスタに接続しました (ワーカーID: {self.worker_id})")
    
    def _join_master(self):
        """タスク管理マスタに参入"""
        # 参入メッセージ送信
        join_message = {
            "type": "JOIN",
            "msg": self.worker_id
        }
        
        message_data = json.dumps(join_message) + '\n'
        self.socket.send(message_data.encode('utf-8'))
        logger.info("参入メッセージを送信しました")
        
        # 参入応答待ち
        try:
            self.socket.settimeout(10.0)  # 10秒タイムアウト
            data = self.socket.recv(1024).decode('utf-8')
            response = json.loads(data)
            
            if response.get("type") == "JOIN_ACK":
                logger.info("参入が承認されました")
                self.socket.settimeout(None)  # タイムアウト解除
            else:
                raise Exception(f"無効な参入応答: {response}")
                
        except Exception as e:
            raise Exception(f"参入処理失敗: {e}")
    
    def _main_loop(self):
        """メインループ - イベント待ち受けと処理"""
        logger.info("待ち受けループを開始")
        
        while self.running:
            try:
                # マスタからのメッセージ受信
                self._receive_master_messages()
                
                # プロセス間メッセージの処理
                self._process_internal_messages()
                
                # 短いスリープで負荷軽減
                time.sleep(0.1)
                
            except Exception as e:
                if self.running:
                    logger.error(f"メインループでエラーが発生: {e}")
                    break
        
        logger.info("待ち受けループを終了")
    
    def _receive_master_messages(self):
        """タスク管理マスタからのメッセージ受信"""
        try:
            self.socket.settimeout(0.1)
            data = self.socket.recv(4096).decode('utf-8', errors='replace')
            
            if data:
                # 改行で区切られた複数のJSONメッセージに対応
                lines = data.strip().split('\n')
                for line in lines:
                    line = line.strip()
                    if line:
                        try:
                            message = json.loads(line)
                            self._handle_master_message(message)
                        except json.JSONDecodeError as je:
                            logger.error(f"JSON解析エラー: {je}")
                            logger.debug(f"受信データ行: {repr(line)}")
                            # JSON解析エラーは続行可能
            else:
                # 接続切断
                logger.info("タスク管理マスタとの接続が切断されました")
                self.running = False
                
        except socket.timeout:
            pass  # タイムアウトは正常
        except Exception as e:
            if self.running:
                logger.error(f"マスタメッセージ受信エラー: {e}")
                self.running = False
    
    def _handle_master_message(self, message: dict):
        """タスク管理マスタからのメッセージを処理"""
        msg_type = message.get("type")
        
        if msg_type == "CHECK":
            # ヘルスチェック
            self._handle_health_check(message)
        
        elif msg_type == "REQUEST":
            # タスク実行依頼
            self._handle_task_request(message)
        
        elif msg_type == "DISCONNECT":
            # 切断通知
            logger.info("切断通知を受信しました") 
            self.running = False
        
        else:
            logger.warning(f"未知のメッセージタイプ: {msg_type}")
    
    def _handle_health_check(self, message: dict):
        """ヘルスチェック処理"""
        req_id = message.get("req_id")
        
        # 即時に応答
        check_ack = {
            "type": "CHECK_ACK",
            "msg": "",
            "req_id": req_id
        }
        
        try:
            message_data = json.dumps(check_ack) + '\n'
            self.socket.send(message_data.encode('utf-8'))
            logger.debug(f"ヘルスチェック応答送信 (req_id: {req_id})")
        except Exception as e:
            logger.error(f"ヘルスチェック応答エラー: {e}")
    
    def _handle_task_request(self, message: dict):
        """タスク実行依頼処理"""
        req_id = message.get("req_id")
        prompt_text = message.get("msg", "")
        
        logger.info(f"タスク実行依頼受信 (req_id: {req_id}, prompt長: {len(prompt_text)}文字)")
        
        # 即時に承諾応答
        request_ack = {
            "type": "REQUEST_ACK",
            "msg": "",
            "req_id": req_id
        }
        
        try:
            message_data = json.dumps(request_ack) + '\n'
            self.socket.send(message_data.encode('utf-8'))
            logger.info(f"タスク実行依頼承諾送信 (req_id: {req_id})")
        except Exception as e:
            logger.error(f"タスク実行依頼承諾エラー: {e}")
            return
        
        # claude管理スレッドにタスク実行依頼を送信
        logger.info(f"タスク実行依頼をclaude管理スレッドに送信 (req_id: {req_id})")
        
        # タスクファイル名を生成
        task_filename = f"task_{req_id}"
        
        with self.claude_lock:
            self.claude_queue.append({
                "type": "REQUEST",
                "msg": prompt_text,
                "req_id": req_id,
                "task_filename": task_filename
            })
    
    def _start_claude_thread(self):
        """
        claude管理スレッドを起動
        claudeプロセスをバックグラウンドで実行し、メッセージを処理
        """
        self.claude_thread = threading.Thread(target=self._claude_thread_main, daemon=True)
        self.claude_thread.start()
        logger.info("claude管理スレッドを起動しました")
        
        # claudeプロセスの起動を待つ
        time.sleep(2)
        if not self.claude_process or self.claude_process.poll() is not None:
            raise Exception("claudeプロセスの起動に失敗しました")
    
    def _claude_thread_main(self):
        """
        claude管理スレッドのメイン処理
        claudeプロセスを起動し、メッセージ待ち受けループを実行
        """
        try:
            # claudeコマンドをバックグラウンドで実行
            cmd = ["claude", "--verbose", "--output-format", "stream-json", "--allowedTools", "WebFetch,Read,Write,Bash"]
            
            # --opusオプションが指定されていた場合、モデルをopusに設定
            if self.use_opus:
                cmd.extend(["--model", "opus"])
                logger.info("Opus4モデルを使用します")
            else:
                logger.info("Sonnet4モデルを使用します（デフォルト）")
            
            logger.info(f"claudeプロセスを起動: {' '.join(cmd)}")
            logger.info(f"実行ディレクトリ: {self.root_dir}")
            
            self.claude_process = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                bufsize=1,
                universal_newlines=True,
                cwd=str(self.root_dir)
            )
            logger.info(f"claudeプロセス開始 (PID: {self.claude_process.pid})")
            
            # 現在実行中のタスク情報
            current_task = None
            
            # メッセージ待ち受けループ
            while self.running:
                # メッセージキューをチェック
                with self.claude_lock:
                    if self.claude_queue:
                        message = self.claude_queue.pop(0)
                    else:
                        message = None
                
                if message:
                    msg_type = message.get("type")
                    
                    if msg_type == "REQUEST":
                        # タスク実行依頼
                        current_task = message
                        prompt_text = message.get("msg", "")
                        req_id = message.get("req_id")
                        
                        logger.info(f"claudeにタスク実行依頼 (req_id: {req_id})")
                        
                        try:
                            # プロンプトをclaudeの標準入力に送信
                            self.claude_process.stdin.write(prompt_text + "\n")
                            self.claude_process.stdin.flush()
                            logger.info(f"プロンプト送信完了 (req_id: {req_id})")
                        except Exception as e:
                            logger.error(f"プロンプト送信エラー: {e}")
                            self._add_message({
                                "type": "FAILED",
                                "msg": current_task.get("task_filename", ""),
                                "req_id": req_id
                            })
                            current_task = None
                    
                    elif msg_type == "EXIT":
                        # 終了メッセージ
                        logger.info("claude管理スレッド終了メッセージを受信")
                        break
                
                # claudeの出力をチェック
                if current_task and self.claude_process:
                    # 標準出力を非ブロッキングで読み取り
                    try:
                        # selectを使用して非ブロッキング読み取り
                        import select
                        readable, _, _ = select.select([self.claude_process.stdout], [], [], 0.1)
                        
                        if readable:
                            line = self.claude_process.stdout.readline()
                            if line:
                                line = line.rstrip()
                                # ストリーム出力のJSONからcontent部分のみを抽出して表示
                                self._process_claude_output_line(line)
                                
                                # タスク完了の判定
                                if self._is_task_complete(line):
                                    logger.info(f"タスク完了を検出 (req_id: {current_task['req_id']})")
                                    
                                    # usage limitチェック
                                    if "usage limit" in line.lower():
                                        result_type = "USAGE_LIMITED"
                                        logger.warning(f"使用制限検出")
                                    else:
                                        result_type = "DONE"
                                    
                                    # 完了通知を送信
                                    self._add_message({
                                        "type": result_type,
                                        "msg": current_task.get("task_filename", ""),
                                        "req_id": current_task['req_id']
                                    })
                                    
                                    # /clearコマンドを送信
                                    try:
                                        self.claude_process.stdin.write("/clear\n")
                                        self.claude_process.stdin.flush()
                                        logger.info("コンテキストをクリアしました")
                                    except Exception as e:
                                        logger.error(f"/clearコマンドエラー: {e}")
                                    
                                    current_task = None
                    
                    except Exception as e:
                        if "Resource temporarily unavailable" not in str(e):
                            logger.error(f"claude出力読み取りエラー: {e}")
                
                # claudeプロセスの状態をチェック
                if self.claude_process and self.claude_process.poll() is not None:
                    exit_code = self.claude_process.poll()
                    logger.error(f"claudeプロセスが異常終了 (exit_code: {exit_code})")
                    
                    # 現在のタスクがあれば失敗通知
                    if current_task:
                        self._add_message({
                            "type": "FAILED",
                            "msg": current_task.get("task_filename", ""),
                            "req_id": current_task['req_id']
                        })
                    break
                
                # 少し待機
                time.sleep(0.1)
            
        except Exception as e:
            logger.error(f"claude管理スレッドエラー: {e}")
        finally:
            # claudeプロセスを終了
            self._terminate_claude_process()
            logger.info("claude管理スレッドを終了しました")
    
    def _is_task_complete(self, line: str) -> bool:
        """
        claudeの出力からタスク完了を判定
        
        Args:
            line: claudeの出力行
            
        Returns:
            bool: タスクが完了した場合True
        """
        try:
            # JSON形式の行をパース
            stream_data = json.loads(line)
            
            # stop_reasonが含まれている場合は完了
            if "message" in stream_data:
                message = stream_data["message"]
                if message.get("stop_reason") is not None:
                    logger.debug(f"タスク完了検出: stop_reason={message['stop_reason']}")
                    return True
        except:
            pass
        
        return False
    
    def _check_usage_limit_in_output(self, output: str) -> bool:
        """
        claude出力からusage limitを検出
        
        Args:
            output: claude実行の標準出力
            
        Returns:
            bool: usage limitが検出された場合True
        """
        if not output:
            return False
        
        # usage limitの検出パターン
        usage_limit_patterns = [
            "usage limit",
            "rate limit",
            "Rate limit",
            "Usage limit",
            "API rate limit",
            "API usage limit",
            "限度",
            "制限"
        ]
        
        # 各行をJSONとしてパースしてcontentをチェック
        for line in output.split('\n'):
            line = line.strip()
            if not line:
                continue
                
            try:
                # JSON形式の行をパース
                stream_data = json.loads(line)
                
                # messageオブジェクトからcontentを抽出
                if "message" in stream_data:
                    message = stream_data["message"]
                    if "content" in message and isinstance(message["content"], list):
                        # content配列の各要素をチェック
                        for content_item in message["content"]:
                            if isinstance(content_item, dict) and content_item.get("type") == "text":
                                text_content = content_item.get("text", "").lower()
                                # usage limitパターンをチェック
                                for pattern in usage_limit_patterns:
                                    if pattern.lower() in text_content:
                                        logger.debug(f"usage limit検出: '{pattern}' in '{text_content[:100]}...'")
                                        return True
                                        
            except json.JSONDecodeError:
                # JSON形式でない行も直接テキストとしてチェック
                line_lower = line.lower()
                for pattern in usage_limit_patterns:
                    if pattern.lower() in line_lower:
                        logger.debug(f"usage limit検出 (非JSON): '{pattern}' in '{line[:100]}...'")
                        return True
            except Exception as e:
                logger.debug(f"usage limit検出処理エラー: {e}")
                continue
        
        return False
    
    def _process_claude_output_line(self, line: str):
        """
        claudeのストリーム出力行を処理してcontentのみを表示
        
        Args:
            line: claudeのストリーム出力の1行（JSON形式）
        """
        try:
            # JSON形式の行をパース
            stream_data = json.loads(line)
            
            # messageオブジェクトからcontentを抽出
            if "message" in stream_data:
                message = stream_data["message"]
                if "content" in message and isinstance(message["content"], list):
                    # content配列の各要素を処理
                    for content_item in message["content"]:
                        if isinstance(content_item, dict):
                            # textタイプの場合はそのまま出力
                            if content_item.get("type") == "text" and "text" in content_item:
                                text_content = content_item["text"]
                                if text_content.strip():  # 空でない場合のみ出力
                                    logger.info(f"[CLAUDE-CONTENT] {text_content}")
                            
                            # tool_useタイプの場合は概要を出力
                            elif content_item.get("type") == "tool_use":
                                tool_name = content_item.get("name", "unknown")
                                tool_id = content_item.get("id", "unknown")
                                logger.info(f"[CLAUDE-TOOL] {tool_name} (id: {tool_id})")
                        
        except json.JSONDecodeError:
            # JSON形式でない行はそのまま出力（デバッグ情報など）
            if line.strip():
                logger.debug(f"[CLAUDE-RAW] {line}")
        except Exception as e:
            # その他のエラーは詳細ログに記録
            logger.debug(f"claude出力処理エラー: {e}, line: {line[:100]}...")
    
    def _process_internal_messages(self):
        """プロセス間メッセージの処理"""
        messages = self._get_messages()
        
        for message in messages:
            msg_type = message.get("type")
            
            if msg_type in ["DONE", "FAILED", "USAGE_LIMITED"]:
                # タスク完了/失敗/使用制限報告をマスタに送信
                self._send_task_result(message)
    
    def _send_task_result(self, message: dict):
        """タスク結果報告をマスタに送信"""
        try:
            msg_type = message.get("type")
            req_id = message.get("req_id", "unknown")
            
            # メッセージタイプによってmsgフィールドを設定
            if msg_type == "USAGE_LIMITED":
                # USAGE_LIMITEDの場合はワーカーIDを送信
                result_message = {
                    "type": msg_type,
                    "msg": self.worker_id
                }
            else:
                # DONE/FAILEDの場合はタスクファイル名を送信
                result_message = {
                    "type": msg_type,
                    "msg": message.get("msg", "")
                }
            
            logger.info(f"タスク結果報告送信準備: {msg_type} (req_id: {req_id})")
            
            message_data = json.dumps(result_message) + '\n'
            self.socket.send(message_data.encode('utf-8'))
            
            logger.info(f"タスク結果報告送信完了: {msg_type} (req_id: {req_id})")
            
        except Exception as e:
            logger.error(f"タスク結果報告エラー: {e}")
    
    def _cleanup(self):
        """クリーンアップ処理"""
        logger.info("クリーンアップ処理を開始")
        
        # claude管理スレッドに終了メッセージを送信
        if self.claude_thread and self.claude_thread.is_alive():
            with self.claude_lock:
                self.claude_queue.append({"type": "EXIT", "msg": ""})
            
            # スレッドの終了を待つ（最大5秒）
            self.claude_thread.join(timeout=5)
            if self.claude_thread.is_alive():
                logger.warning("claude管理スレッドの終了がタイムアウトしました")
        
        # claudeプロセスを停止
        self._terminate_claude_process()
        
        # ソケット切断
        if self.socket:
            try:
                self.socket.close()
            except Exception as e:
                logger.error(f"ソケット切断エラー: {e}")
        
        logger.info("タスクワーカーを終了しました")


def main():
    """メイン関数"""
    parser = argparse.ArgumentParser(description='タスクワーカー')
    parser.add_argument(
        'host',
        nargs='?',
        default='localhost',
        help='タスク管理マスタのホスト名 (デフォルト: localhost)'
    )
    parser.add_argument(
        'port',
        type=int,
        nargs='?',
        default=34567,
        help='タスク管理マスタのポート番号 (デフォルト: 34567)'
    )
    parser.add_argument(
        '--root-dir',
        type=str,
        help='ルートディレクトリパス (デフォルト: カレントディレクトリ)'
    )
    parser.add_argument(
        '--opus',
        action='store_true',
        help='Opus4モデルを使用する (デフォルト: Sonnet4)'
    )
    
    args = parser.parse_args()
    
    try:
        worker = TaskWorker(host=args.host, port=args.port, root_dir=args.root_dir, use_opus=args.opus)
        worker.start()
    except KeyboardInterrupt:
        logger.info("Ctrl-Cで終了")
    except Exception as e:
        logger.error(f"予期しないエラー: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()