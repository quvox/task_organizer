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
    
    def __init__(self, host: str = "localhost", port: int = 34567, root_dir: Optional[str] = None):
        """
        タスクワーカーを初期化
        
        Args:
            host: タスク管理マスタのホスト名 (デフォルト: localhost)
            port: タスク管理マスタのポート番号 (デフォルト: 34567)
            root_dir: ルートディレクトリパス (デフォルト: カレントディレクトリ)
        """
        self.host = host
        self.port = port
        self.root_dir = Path(root_dir) if root_dir else Path.cwd()
        self.socket: Optional[socket.socket] = None
        self.worker_id: str = ""
        self.running = True
        self.current_process: Optional[subprocess.Popen] = None
        self.message_queue = []
        self.message_lock = threading.Lock()
        
        # シグナルハンドラ設定
        signal.signal(signal.SIGINT, self._signal_handler)
        signal.signal(signal.SIGTERM, self._signal_handler)
    
    def _signal_handler(self, signum, frame):
        """シグナルハンドラ - Ctrl-C等での終了処理"""
        logger.info("終了シグナルを受信しました")
        self.running = False
        self._terminate_current_process()
        if self.socket:
            try:
                # 離脱メッセージ送信
                leave_message = {"type": "LEAVE", "msg": ""}
                self.socket.send(json.dumps(leave_message).encode('utf-8'))
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
    
    def _terminate_current_process(self):
        """現在実行中のプロセスを強制停止"""
        if self.current_process and self.current_process.poll() is None:
            try:
                self.current_process.terminate()
                # 2秒待って強制終了
                try:
                    self.current_process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    self.current_process.kill()
                logger.info("実行中プロセスを停止しました")
            except Exception as e:
                logger.error(f"プロセス停止エラー: {e}")
            finally:
                self.current_process = None
    
    def start(self):
        """タスクワーカーを開始"""
        logger.info(f"タスクワーカーを開始 (接続先: {self.host}:{self.port})")
        
        try:
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
        
        self.socket.send(json.dumps(join_message).encode('utf-8'))
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
                try:
                    message = json.loads(data)
                    self._handle_master_message(message)
                except json.JSONDecodeError as je:
                    logger.error(f"JSON解析エラー: {je}")
                    logger.debug(f"受信データ: {repr(data)}")
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
            self.socket.send(json.dumps(check_ack).encode('utf-8'))
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
            self.socket.send(json.dumps(request_ack).encode('utf-8'))
            logger.info(f"タスク実行依頼承諾送信 (req_id: {req_id})")
        except Exception as e:
            logger.error(f"タスク実行依頼承諾エラー: {e}")
            return
        
        # 別スレッドでタスク実行
        logger.info(f"タスク実行スレッド開始 (req_id: {req_id})")
        task_thread = threading.Thread(
            target=self._execute_task,
            args=(prompt_text, req_id),
            daemon=True
        )
        task_thread.start()
        logger.info(f"タスク実行スレッド起動完了 (req_id: {req_id})")
    
    def _execute_task(self, prompt_text: str, req_id: str):
        """タスク実行スレッド"""
        try:
            logger.info(f"タスク実行開始 (req_id: {req_id})")
            logger.debug(f"プロンプトテキスト: {prompt_text[:100]}...")
            
            # claudeコマンド実行
            cmd = ["claude", "--verbose", "--output-format", "stream-json", "--allowedTools", "WebFetch,Read,Write,Bash", "-p", prompt_text]
            logger.info(f"実行コマンド: {' '.join(cmd[:4])} [プロンプト省略]")
            logger.info(f"実行ディレクトリ: {self.root_dir}")
            
            self.current_process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                bufsize=1,  # 行バッファリング
                universal_newlines=True,
                cwd=str(self.root_dir)  # ルートディレクトリで実行
            )
            logger.info(f"claudeプロセス開始 (PID: {self.current_process.pid})")
            
            # リアルタイムで出力を監視
            logger.info(f"claudeプロセス出力監視開始 (req_id: {req_id})")
            
            # 出力監視スレッドを開始
            output_lines = []
            error_lines = []
            
            def read_stdout():
                try:
                    for line in iter(self.current_process.stdout.readline, ''):
                        if line:
                            line = line.rstrip()
                            output_lines.append(line)
                            # ストリーム出力のJSONからcontent部分のみを抽出して表示
                            self._process_claude_output_line(line)
                        if self.current_process.poll() is not None:
                            break
                except Exception as e:
                    logger.error(f"標準出力読み取りエラー: {e}")
            
            def read_stderr():
                try:
                    for line in iter(self.current_process.stderr.readline, ''):
                        if line:
                            line = line.rstrip()
                            error_lines.append(line)
                            logger.warning(f"[CLAUDE-ERR] {line}")
                        if self.current_process.poll() is not None:
                            break
                except Exception as e:
                    logger.error(f"標準エラー読み取りエラー: {e}")
            
            # 出力監視スレッド開始
            stdout_thread = threading.Thread(target=read_stdout, daemon=True)
            stderr_thread = threading.Thread(target=read_stderr, daemon=True)
            stdout_thread.start()
            stderr_thread.start()
            
            # プロセス完了を待機
            logger.info(f"claudeプロセス完了待機中 (req_id: {req_id})")
            exit_code = self.current_process.wait()
            
            # 出力監視スレッドの完了を待つ
            stdout_thread.join(timeout=2)
            stderr_thread.join(timeout=2)
            
            logger.info(f"claudeプロセス完了 (req_id: {req_id}, exit_code: {exit_code})")
            logger.info(f"標準出力行数: {len(output_lines)}, 標準エラー行数: {len(error_lines)}")
            
            # 出力とエラーを結合
            stdout = '\n'.join(output_lines)
            stderr = '\n'.join(error_lines)
            
            # 実行結果の判定
            if exit_code == 0:
                result_type = "DONE"
                logger.info(f"タスク完了 (req_id: {req_id})")
                if stdout:
                    logger.info(f"最終出力 (最初の100文字): {stdout[:100]}...")
            else:
                result_type = "FAILED"
                logger.info(f"タスク失敗 (req_id: {req_id}, exit_code: {exit_code})")
                if stderr:
                    logger.error(f"最終エラー出力: {stderr}")
                if stdout:
                    logger.warning(f"部分的出力: {stdout[:100]}...")
            
            # 完了通知をキューに追加
            logger.info(f"完了通知をキューに追加 (type: {result_type}, req_id: {req_id})")
            self._add_message({
                "type": result_type,
                "msg": f"task_{req_id}",  # タスクファイル名として使用
                "req_id": req_id
            })
            
        except Exception as e:
            logger.error(f"タスク実行エラー (req_id: {req_id}): {e}")
            # 失敗通知をキューに追加
            self._add_message({
                "type": "FAILED",
                "msg": f"task_{req_id}",
                "req_id": req_id
            })
        finally:
            self.current_process = None
            logger.info(f"タスク実行スレッド終了 (req_id: {req_id})")
    
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
            
            if msg_type in ["DONE", "FAILED"]:
                # タスク完了/失敗報告をマスタに送信
                self._send_task_result(message)
    
    def _send_task_result(self, message: dict):
        """タスク結果報告をマスタに送信"""
        try:
            # msgフィールドとreq_idを除去してマスタ用メッセージ作成
            result_message = {
                "type": message.get("type"),
                "msg": message.get("msg", "")
            }
            
            req_id = message.get("req_id", "unknown")
            logger.info(f"タスク結果報告送信準備: {message.get('type')} (req_id: {req_id})")
            
            json_data = json.dumps(result_message)
            self.socket.send(json_data.encode('utf-8'))
            
            logger.info(f"タスク結果報告送信完了: {message.get('type')} (req_id: {req_id})")
            
        except Exception as e:
            logger.error(f"タスク結果報告エラー: {e}")
    
    def _cleanup(self):
        """クリーンアップ処理"""
        logger.info("クリーンアップ処理を開始")
        
        # 実行中プロセスを停止
        self._terminate_current_process()
        
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
    
    args = parser.parse_args()
    
    try:
        worker = TaskWorker(host=args.host, port=args.port, root_dir=args.root_dir)
        worker.start()
    except KeyboardInterrupt:
        logger.info("Ctrl-Cで終了")
    except Exception as e:
        logger.error(f"予期しないエラー: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()