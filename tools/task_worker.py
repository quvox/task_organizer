#!/usr/bin/env python3
"""
タスクワーカー

タスクワーカーは、タスク管理マスターに接続し、タスク実行依頼を受信して
Claude Code SDKを使用したAIエージェントで処理を実行するワーカープロセス

主な機能:
- タスク管理マスターとのTCP通信
- 参入処理と参入応答待ち
- ヘルスチェック対応
- タスク実行依頼の受信と処理
- Claude管理スレッドによるAIエージェント実行
- プロセス管理とシグナルハンドリング
"""

import argparse
import asyncio
import json
import logging
import os
import signal
import socket
import sys
import threading
import time
from pathlib import Path
from typing import Optional, Dict, Any, List
import traceback

# Claude Code SDKの導入（ドキュメント: 65行目）
try:
    from claude_code_sdk import query, ClaudeCodeOptions
    CLAUDE_SDK_AVAILABLE = True
except ImportError:
    CLAUDE_SDK_AVAILABLE = False

# ログ設定
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)


class ClaudeManagerThread:
    """
    Claude管理スレッドクラス（ドキュメント: 63行目）
    
    Claude Code SDKを用いてAIエージェントを動作させる
    長期間動作するアプリケーションを意識し、multi-turnで実行する
    """
    
    def __init__(self, worker_id: str, root_dir: str, use_opus: bool = False, debug_auth: bool = False, skip_auth_check: bool = False):
        """
        Claude管理スレッドを初期化
        
        Args:
            worker_id: ワーカーID
            root_dir: ルートディレクトリ（claudeが実行されるディレクトリ）
            use_opus: Opusモデルを使用するかどうか
            debug_auth: 認証デバッグモードかどうか
            skip_auth_check: 認証チェックをスキップするかどうか
        """
        self.worker_id = worker_id
        self.root_dir = Path(root_dir)
        self.use_opus = use_opus
        self.debug_auth = debug_auth
        self.skip_auth_check = skip_auth_check
        self.running = True
        self.message_queue = []
        self.message_lock = threading.Lock()
        self.result_callback = None
        
        # モデル設定（ドキュメント: 79行目）
        self.model = "opus" if use_opus else "sonnet"
        
        # 会話コンテキスト管理
        self.has_active_conversation = False  # 初回実行かどうかのフラグ
        
        # Claude Code SDKの設定
        self.claude_options = None
        self._initialize_claude_sdk()
    
    def _initialize_claude_sdk(self):
        """Claude Code SDKを初期化（ドキュメント: 65行目）"""
        if not CLAUDE_SDK_AVAILABLE:
            raise Exception("Claude Code SDKが利用できません。SDKをインストールしてください。")
        
        # claudeログイン状態の確認（ドキュメント: 71行目）
        if not self.skip_auth_check:
            if self._check_claude_login():
                logger.info("Claude認証状態確認完了")
            else:
                logger.warning("Claude認証状態の確認でエラーが発生しましたが、実行を継続します")
                logger.warning("認証エラーが発生した場合は --skip-auth-check オプションを使用してください")
        else:
            logger.warning("認証チェックをスキップしました")
        
        # APIキーの使用チェック（ドキュメント: 73行目）
        if os.environ.get('ANTHROPIC_API_KEY'):
            logger.warning("APIキーを用いないように警告を表示")
            raise Exception("APIキーを用いないように警告します。")
        
        # Claude Code SDKオプション設定（ドキュメント: 77行目）
        self.claude_options = ClaudeCodeOptions(
            model=self.model,
            cwd=str(self.root_dir),
            allowed_tools=["WebFetch", "Read", "Write", "Bash"],  # ドキュメント: 77行目
            continue_conversation=True  # 常にTrueで会話を継続（ドキュメント: 81行目）
        )
        
        logger.info(f"Claude Code SDK初期化完了 (モデル: {self.model}, ディレクトリ: {self.root_dir})")
    
    def _check_claude_login(self) -> bool:
        """
        claudeログイン状態を確認（ドキュメント: 71行目）
        
        Claude Code SDKの初期化が可能かどうかを確認する
        実際のクエリ実行は行わず、初期化のみで判断する
        
        Returns:
            bool: ログイン済みの場合True
        """
        try:
            if self.debug_auth:
                logger.info("=== Claude認証デバッグ情報 ===")
                logger.info("Claude Code SDKの初期化可能性をテスト中...")
            
            # Claude Code SDKオプションの作成をテスト
            test_options = ClaudeCodeOptions(
                model=self.model,
                cwd=str(self.root_dir),
                allowed_tools=[],  # ツールなしで最小限のテスト
                max_turns=1
            )
            
            if self.debug_auth:
                logger.info("Claude Code SDKオプション作成成功")
                logger.info("=== Claude認証デバッグ終了 ===")
            
            logger.info("Claude Code SDK初期化可能性確認完了")
            return True
            
        except Exception as e:
            if self.debug_auth:
                logger.error(f"Claude認証確認エラー: {e}")
                import traceback
                logger.error(f"詳細エラー: {traceback.format_exc()}")
                logger.info("=== Claude認証デバッグ終了 ===")
            else:
                logger.warning(f"Claude認証確認でエラーが発生: {e}")
            return False
    
    def set_result_callback(self, callback):
        """結果通知コールバックを設定"""
        self.result_callback = callback
    
    def add_message(self, message: Dict[str, Any]):
        """メッセージをキューに追加"""
        with self.message_lock:
            self.message_queue.append(message)
    
    def start(self):
        """スレッドを開始"""
        self.thread = threading.Thread(target=self._run, daemon=True)
        self.thread.start()
        logger.info("Claude管理スレッドを開始しました")
    
    def stop(self):
        """スレッドを停止"""
        self.running = False
        self.add_message({"type": "EXIT", "msg": ""})
    
    def _run(self):
        """
        Claude管理スレッドのメイン処理
        メッセージ待ち受けループ（ドキュメント: 83行目）
        """
        logger.info("Claude管理スレッドのメッセージ待ち受けループを開始")
        
        try:
            while self.running:
                # メッセージ待ち受け（ドキュメント: 83-87行目）
                message = self._get_next_message()
                
                if message:
                    msg_type = message.get("type")
                    
                    if msg_type == "REQUEST":
                        # タスク実行依頼（ドキュメント: 85行目）
                        self._handle_task_request(message)
                    
                    elif msg_type == "EXIT":
                        # 終了メッセージ（ドキュメント: 86行目）
                        logger.info("Claude管理スレッド終了メッセージを受信")
                        break
                
                # 短い間隔でポーリング
                time.sleep(0.1)
                
        except Exception as e:
            logger.error(f"Claude管理スレッドエラー: {e}")
            logger.error(traceback.format_exc())
        finally:
            logger.info("Claude管理スレッドを終了しました")
    
    def _get_next_message(self) -> Optional[Dict[str, Any]]:
        """次のメッセージを取得"""
        with self.message_lock:
            if self.message_queue:
                return self.message_queue.pop(0)
        return None
    
    def _handle_task_request(self, message: Dict[str, Any]):
        """
        タスク実行依頼を処理（ドキュメント: 93行目）
        
        Args:
            message: タスク実行依頼メッセージ
        """
        req_id = message.get("req_id", "unknown")
        prompt_text = message.get("msg", "")
        task_filename = message.get("task_filename", f"task_{req_id}")
        
        logger.info(f"[CLAUDE-THREAD] タスク実行依頼を受信 (req_id: {req_id}, task: {task_filename})")
        
        # タスク実行を別スレッドで非同期実行（ドキュメント26行目：長時間ブロッキング防止）
        import threading
        task_thread = threading.Thread(
            target=self._execute_task_async,
            args=(prompt_text, req_id, task_filename),
            daemon=True,
            name=f"task-{req_id}"
        )
        task_thread.start()
        logger.info(f"[CLAUDE-THREAD] タスク実行スレッドを起動 (req_id: {req_id}, スレッド: {task_thread.name})")
        logger.debug("[CLAUDE-THREAD] Claude管理スレッドのメッセージループは継続中")
    
    def _execute_task_async(self, prompt_text: str, req_id: str, task_filename: str):
        """
        タスクを非同期で実行（別スレッドで実行される）
        
        Args:
            prompt_text: プロンプトテキスト
            req_id: リクエストID
            task_filename: タスクファイル名
        """
        try:
            logger.info(f"[TASK-ASYNC] タスク実行開始 (req_id: {req_id})")
            
            # AIエージェントにプロンプトを与える（ドキュメント: 93行目）
            # JSONデコードエラーの場合はリトライ
            max_retries = 3
            retry_count = 0
            result_type = "FAILED"
            
            while retry_count < max_retries:
                try:
                    result_type = self._execute_task(prompt_text, req_id)
                    break  # 成功したらループを抜ける
                except Exception as e:
                    if "JSONDecodeError" in str(e) and retry_count < max_retries - 1:
                        retry_count += 1
                        logger.warning(f"[TASK-ASYNC-RETRY-{req_id}] JSONデコードエラーのためリトライ ({retry_count}/{max_retries})")
                        time.sleep(2 * retry_count)  # バックオフ待機
                    else:
                        raise  # リトライ不可なエラーまたはリトライ回数上限
            
            logger.info(f"[TASK-ASYNC] タスク実行完了 (req_id: {req_id}, 結果: {result_type}, リトライ: {retry_count})")
            
            # タスク実行完了通知（ドキュメント: 107-111行目）
            if self.result_callback:
                self.result_callback({
                    "type": result_type,
                    "msg": task_filename,
                    "req_id": req_id
                })
            
            # コンテキストを消去（ドキュメント: 113行目）
            self._clear_context()
            
        except Exception as e:
            logger.error(f"[TASK-ASYNC] タスク実行エラー (req_id: {req_id}): {e}")
            logger.error(traceback.format_exc())
            
            # タスク失敗通知
            if self.result_callback:
                self.result_callback({
                    "type": "FAILED",
                    "msg": task_filename,
                    "req_id": req_id
                })
    
    def _execute_task(self, prompt_text: str, req_id: str) -> str:
        """
        タスクを実行
        
        Args:
            prompt_text: プロンプトテキスト
            req_id: リクエストID
            
        Returns:
            str: 結果タイプ（DONE, FAILED, USAGE_LIMITED）
        """
        try:
            # スレッド内で新しいイベントループを作成して実行
            # asyncio.run()は新しいイベントループを作成するため、
            # 既存のイベントループの有無を気にする必要はない
            logger.debug(f"新しいイベントループでClaudeクエリを実行 (req_id: {req_id})")
            result = asyncio.run(self._execute_claude_query(prompt_text, req_id))
            return result
            
        except Exception as e:
            logger.error(f"タスク実行エラー: {e}")
            logger.error(traceback.format_exc())
            return "FAILED"
    
    async def _execute_claude_query(self, prompt_text: str, req_id: str) -> str:
        """
        Claude Code SDKを使用してクエリを実行
        
        Args:
            prompt_text: プロンプトテキスト
            req_id: リクエストID
            
        Returns:
            str: 結果タイプ（DONE, FAILED, USAGE_LIMITED）
        """
        try:
            logger.info(f"Claude Code SDK実行開始 (req_id: {req_id})")
            logger.debug(f"[CLAUDE-PROMPT-{req_id}] \u30d7\u30ed\u30f3\u30d7\u30c8\u5185\u5bb9:\n{prompt_text}")
            logger.debug(f"[CLAUDE-OPTIONS-{req_id}] continue_conversation={self.claude_options.continue_conversation}")
            
            # continue_conversationオプションの設定（ドキュメント：81行目）
            # 2回目以降のプロンプトではcontinueオプションを指定
            # これは、claude初回起動時のシステムプロンプトの実行による
            # オーバーヘッドを削減するため（ドキュメント: 81行目）
            if self.has_active_conversation:
                # 既存の会話を継続
                logger.info(f"[会話継続] continue_conversation=True (req_id: {req_id})")
            else:
                # 初回実行時
                logger.info(f"[初回実行] continue_conversation=Trueを設定 (req_id: {req_id})")
                logger.debug(f"[CLAUDE-FIRST-RUN-{req_id}] Claudeの初回起動です。システムプロンプトが読み込まれます。")
                self.has_active_conversation = True
            
            # 常にcontinue_conversation=Trueを設定
            self.claude_options.continue_conversation = True
            
            # Claude Code SDKでクエリ実行
            message_count = 0
            usage_limited = False
            
            try:
                async for message in query(prompt=prompt_text, options=self.claude_options):
                    message_count += 1
                    
                    # メッセージ処理（ドキュメント: 95行目）
                    self._process_claude_message(message, req_id)
                    
                    # usage limit検出
                    if self._check_usage_limit(message):
                        usage_limited = True
                        logger.warning(f"使用制限検出 (req_id: {req_id})")
                        break
                    
                    # 進捗ログ（5メッセージごと）
                    if message_count % 5 == 0:
                        logger.debug(f"[CLAUDE-PROGRESS-{req_id}] 処理中... (メッセージ数: {message_count})")
            except Exception as query_error:
                # query実行中のエラーをキャッチ
                logger.error(f"[CLAUDE-QUERY-ERROR-{req_id}] query実行中にエラー: {query_error}")
                # メッセージがある程度処理されていれば、部分的に成功とみなす
                if message_count > 0:
                    logger.warning(f"[CLAUDE-PARTIAL-{req_id}] {message_count}個のメッセージを処理後にエラー発生")
                raise  # エラーを再度raiseして外側のexceptで処理
            
            logger.info(f"Claude Code SDK実行完了 (req_id: {req_id}, messages: {message_count})")
            logger.debug(f"[CLAUDE-COMPLETE-{req_id}] 総メッセージ数: {message_count}, usage_limited: {usage_limited}")
            
            # 会話は常にアクティブのまま維持（ドキュメント: 81, 113行目）

            # 結果判定
            if usage_limited:
                return "USAGE_LIMITED"
            else:
                return "DONE"
                
        except Exception as e:
            error_type = type(e).__name__
            logger.error(f"Claude Code SDK実行エラー (req_id: {req_id}, type: {error_type}): {e}")
            logger.debug(f"[CLAUDE-EXEC-ERROR-{req_id}] {traceback.format_exc()}")
            
            # JSONデコードエラーの場合は、リトライ可能なエラーとして扱う
            if "JSONDecodeError" in str(e) or "unhandled errors in a TaskGroup" in str(e):
                logger.warning(f"[CLAUDE-RETRY-{req_id}] JSONデコードエラーが発生しました。Claude SDKの通信エラーの可能性があります。")
                # エラーを再度raiseして、上位でリトライ処理
                raise
            
            # その他のエラー
            return "FAILED"
    
    def _process_claude_message(self, message: Dict[str, Any], req_id: str):
        """
        Claudeからの出力を処理（ドキュメント: 95行目）
        JSONオブジェクト内のcontentの中だけを出力
        
        Args:
            message: Claudeからのメッセージ
            req_id: リクエストID
        """
        try:
            # Claudeからの生メッセージをデバッグログに出力
            # Claude SDKのメッセージオブジェクトは直接JSON化できないため、
            # 文字列表現または属性をチェックして出力
            try:
                # メッセージが辞書の場合はそのままJSON化
                if isinstance(message, dict):
                    logger.debug(f"[CLAUDE-RAW-{req_id}] {json.dumps(message, ensure_ascii=False, indent=2)}")
                else:
                    # オブジェクトの場合は属性を辞書に変換して出力
                    message_dict = {}
                    if hasattr(message, '__dict__'):
                        message_dict = message.__dict__.copy()
                    else:
                        # オブジェクトの型と文字列表現を出力
                        message_dict = {
                            'type': type(message).__name__,
                            'str': str(message)
                        }
                    logger.debug(f"[CLAUDE-RAW-{req_id}] {message_dict}")
            except Exception as e:
                logger.debug(f"[CLAUDE-RAW-{req_id}] メッセージデバッグ出力エラー: {e}, メッセージ型: {type(message)}")
            
            # Claude SDKのメッセージオブジェクトを辞書に変換
            if not isinstance(message, dict):
                # SystemMessage, AssistantMessage, ResultMessageなどのオブジェクトを辞書に変換
                msg_type = type(message).__name__.lower()
                if msg_type == 'systemmessage':
                    msg_type = 'system'
                    # システムメッセージ（システムプロンプト）をデバッグログに出力
                    if hasattr(message, 'content'):
                        logger.debug(f"[CLAUDE-SYSTEM-PROMPT-{req_id}] \u30b7\u30b9\u30c6\u30e0\u30d7\u30ed\u30f3\u30d7\u30c8: {message.content}")
                    elif hasattr(message, 'message') and hasattr(message.message, 'content'):
                        logger.debug(f"[CLAUDE-SYSTEM-PROMPT-{req_id}] \u30b7\u30b9\u30c6\u30e0\u30d7\u30ed\u30f3\u30d7\u30c8: {message.message.content}")
                elif msg_type == 'assistantmessage':
                    msg_type = 'assistant'
                elif msg_type == 'resultmessage':
                    msg_type = 'tool_result'
                
                # メッセージを辞書形式に変換
                message_dict = {
                    'type': msg_type
                }
                
                # メッセージの属性を取得
                if hasattr(message, 'message'):
                    message_dict['message'] = message.message
                if hasattr(message, 'content'):
                    message_dict['content'] = message.content
                if hasattr(message, 'tool_use_id'):
                    message_dict['tool_use_id'] = message.tool_use_id
                if hasattr(message, 'error'):
                    message_dict['error'] = message.error
                
                message = message_dict
            
            if isinstance(message, dict):
                msg_type = message.get("type", "")
                
                # assistantメッセージの処理
                if msg_type == "assistant":
                    msg_data = message.get("message", {})
                    content_list = msg_data.get("content", [])
                    
                    for content_item in content_list:
                        if isinstance(content_item, dict):
                            item_type = content_item.get("type", "")
                            
                            # テキストコンテンツの出力（ドキュメント: 95行目）
                            if item_type == "text":
                                text = content_item.get("text", "")
                                if text.strip():
                                    logger.info(f"[CLAUDE-{req_id}] {text}")
                                    # デバッグ用に詳細も出力
                                    logger.debug(f"[CLAUDE-TEXT-{req_id}] \u6587字数: {len(text)}, \u5185\u5bb9: {repr(text[:200])}..." if len(text) > 200 else f"[CLAUDE-TEXT-{req_id}] \u6587\u5b57\u6570: {len(text)}, \u5185\u5bb9: {repr(text)}")
                            
                            # ツール使用の表示
                            elif item_type == "tool_use":
                                tool_name = content_item.get("name", "unknown")
                                tool_id = content_item.get("id", "unknown")
                                tool_input = content_item.get("input", {})
                                logger.info(f"[CLAUDE-TOOL-{req_id}] {tool_name} (id: {tool_id})")
                                # ツール入力をデバッグログに出力
                                logger.debug(f"[CLAUDE-TOOL-INPUT-{req_id}] {tool_name}: {json.dumps(tool_input, ensure_ascii=False, indent=2)}")
                
                # ツール結果の表示
                elif msg_type == "tool_result":
                    tool_id = message.get("tool_use_id", "unknown")
                    tool_result = message.get("content", [])
                    logger.debug(f"[CLAUDE-TOOL-RESULT-{req_id}] Tool ID: {tool_id}")
                    # ツール結果の詳細をデバッグログに出力
                    if isinstance(tool_result, list):
                        for result_item in tool_result:
                            if isinstance(result_item, dict):
                                result_type = result_item.get("type", "")
                                if result_type == "text":
                                    result_text = result_item.get("text", "")
                                    if len(result_text) > 500:
                                        logger.debug(f"[CLAUDE-TOOL-RESULT-TEXT-{req_id}] {result_text[:500]}...")
                                    else:
                                        logger.debug(f"[CLAUDE-TOOL-RESULT-TEXT-{req_id}] {result_text}")
                
                # システムメッセージの表示
                elif msg_type == "system":
                    # システムプロンプトの内容をデバッグログに出力
                    content = message.get("content", "")
                    if isinstance(content, str):
                        logger.debug(f"[CLAUDE-SYSTEM-PROMPT-{req_id}] {content}")
                    elif isinstance(content, list):
                        for item in content:
                            if isinstance(item, dict) and item.get("type") == "text":
                                logger.debug(f"[CLAUDE-SYSTEM-PROMPT-{req_id}] {item.get('text', '')}")
                
                # エラーメッセージの表示
                elif msg_type == "error":
                    error = message.get("error", {})
                    logger.error(f"[CLAUDE-ERROR-{req_id}] {error}")
                    # エラーの詳細をデバッグログに出力
                    logger.debug(f"[CLAUDE-ERROR-DETAIL-{req_id}] {json.dumps(error, ensure_ascii=False, indent=2)}")
                
                # その他のメッセージタイプ
                else:
                    logger.debug(f"[CLAUDE-OTHER-{req_id}] type: {msg_type}")
                    
        except Exception as e:
            logger.error(f"Claudeメッセージ処理エラー (req_id: {req_id}): {e}")
            logger.debug(f"[CLAUDE-PROCESS-ERROR-{req_id}] {traceback.format_exc()}")
    
    def _check_usage_limit(self, message: Dict[str, Any]) -> bool:
        """
        usage limitを検出（ドキュメント: 110行目）
        
        Args:
            message: Claudeからのメッセージ
            
        Returns:
            bool: usage limitが検出された場合True
        """
        try:
            if isinstance(message, dict):
                # エラーメッセージのチェック
                error_msg = str(message.get("error", "")).lower()
                if "usage limit" in error_msg or "rate limit" in error_msg:
                    return True
                
                # contentのチェック
                if message.get("type") == "assistant":
                    msg_data = message.get("message", {})
                    content_list = msg_data.get("content", [])
                    
                    for content_item in content_list:
                        if isinstance(content_item, dict) and content_item.get("type") == "text":
                            text = content_item.get("text", "").lower()
                            if "usage limit" in text or "rate limit" in text:
                                return True
                                
        except Exception as e:
            logger.debug(f"usage limitチェックエラー: {e}")
        
        return False
    
    def _clear_context(self):
        """
        コンテキストを消去（ドキュメント: 113行目）
        /clearコマンドを実行
        """
        try:
            # 常に/clearコマンドを実行（ドキュメント: 113行目）
            logger.info("コンテキストクリア実行中...")

            # /clearコマンドを実行（continueオプション付き）
            # 次にタスク実行依頼を受けた時に、コンテキストがクリアされた
            # クリーンな状態でタスクを実行できるようにする（ドキュメント: 113行目）
            self.claude_options.continue_conversation = True

            # 非同期処理を同期的に実行
            # asyncio.run()を使用して新しいイベントループを作成
            import asyncio
            try:
                async def clear_async():
                    async for message in query(prompt="/clear", options=self.claude_options):
                        # /clearコマンドの出力をログに記録
                        self._process_clear_message(message)

                asyncio.run(clear_async())

                # コンテキストはクリアされるが、会話は継続
                logger.info("コンテキストクリア完了（会話は継続中）")

            except Exception as e:
                logger.error(f"/clearコマンド実行エラー: {e}")
                # エラーが発生しても会話は継続

        except Exception as e:
            logger.error(f"コンテキストクリアエラー: {e}")
    
    def _process_clear_message(self, message):
        """
        /clearコマンドの出力を処理
        """
        try:
            # Claude SDKのメッセージオブジェクトを処理
            if not isinstance(message, dict):
                # オブジェクトの場合は辞書に変換
                if hasattr(message, 'message') and hasattr(message.message, 'content'):
                    content_list = message.message.content
                    for content_item in content_list:
                        if hasattr(content_item, 'type') and content_item.type == 'text':
                            if hasattr(content_item, 'text') and content_item.text.strip():
                                logger.debug(f"[CLEAR] {content_item.text}")
            else:
                # 辞書の場合の処理
                msg_type = message.get("type", "")
                if msg_type == "assistant":
                    msg_data = message.get("message", {})
                    content_list = msg_data.get("content", [])
                    for content_item in content_list:
                        if isinstance(content_item, dict) and content_item.get("type") == "text":
                            text = content_item.get("text", "")
                            if text.strip():
                                logger.debug(f"[CLEAR] {text}")
        except Exception as e:
            logger.debug(f"[CLEAR] メッセージ処理エラー: {e}")


class TaskWorker:
    """
    タスクワーカーのメインクラス
    
    タスク管理マスターと接続し、タスク実行依頼を受信して処理する
    """
    
    def __init__(self, host: str = "localhost", port: int = 34567, 
                 root_dir: Optional[str] = None, use_opus: bool = False, debug_auth: bool = False, skip_auth_check: bool = False):
        """
        タスクワーカーを初期化
        
        Args:
            host: タスク管理マスターのホスト名（デフォルト: localhost）
            port: タスク管理マスターのポート番号（デフォルト: 34567）
            root_dir: ルートディレクトリパス（デフォルト: カレントディレクトリ）
            use_opus: Opusモデルを使用するかどうか（デフォルト: False）
            debug_auth: 認証デバッグモードかどうか（デフォルト: False）
            skip_auth_check: 認証チェックをスキップするかどうか（デフォルト: False）
        """
        self.host = host
        self.port = port
        self.root_dir = Path(root_dir) if root_dir else Path.cwd()
        self.use_opus = use_opus
        self.debug_auth = debug_auth
        self.skip_auth_check = skip_auth_check
        
        # ネットワーク関連
        self.socket: Optional[socket.socket] = None
        self.worker_id: str = ""
        self.running = True
        self._recv_buffer = ""
        
        # Claude管理スレッド
        self.claude_manager: Optional[ClaudeManagerThread] = None
        
        # メッセージキュー（Claude管理スレッドからの結果）
        self.result_queue = []
        self.result_lock = threading.Lock()
        
        # REQUESTメッセージ受信追跡
        self.last_request_time = None
        self.request_count = 0
        
        # シグナルハンドラ設定
        signal.signal(signal.SIGINT, self._signal_handler)
        signal.signal(signal.SIGTERM, self._signal_handler)
    
    def _signal_handler(self, signum, frame):
        """
        シグナルハンドラ - Ctrl-C等での終了処理（ドキュメント: 59行目）
        """
        logger.info("終了シグナルを受信しました")
        self.running = False
        
        # Claude管理スレッドの強制停止（ドキュメント: 55行目）
        if self.claude_manager:
            self.claude_manager.stop()
    
    def start(self):
        """
        タスクワーカーを開始
        """
        logger.info(f"タスクワーカーを開始 (接続先: {self.host}:{self.port})")
        
        try:
            # 準備処理（ドキュメント: 15行目）
            self._initialize()
            
            # メインループ（ドキュメント: 26行目）
            self._main_loop()
            
        except Exception as e:
            logger.error(f"タスクワーカーエラー: {e}")
            logger.error(traceback.format_exc())
        finally:
            self._cleanup()
    
    def _initialize(self):
        """
        準備処理（ドキュメント: 15行目）
        """
        # Claude管理スレッドを非同期的に立ち上げ（ドキュメント: 17行目）
        self._start_claude_manager()
        
        # タスク管理マスターとのTCP接続を確立（ドキュメント: 18行目）
        self._connect_to_master()
        
        # 参入処理（ドキュメント: 18行目）
        self._join_master()
    
    def _start_claude_manager(self):
        """
        Claude管理スレッドを非同期的に立ち上げ（ドキュメント: 17行目）
        """
        try:
            # ワーカーIDは後で設定されるため、一時的に空文字列で初期化
            self.claude_manager = ClaudeManagerThread(
                worker_id="",  # 後でTCP接続時に設定
                root_dir=str(self.root_dir),
                use_opus=self.use_opus,
                debug_auth=self.debug_auth,
                skip_auth_check=self.skip_auth_check
            )
            
            # 結果通知コールバックを設定
            self.claude_manager.set_result_callback(self._handle_claude_result)
            
            # Claude管理スレッドを非同期的に開始
            self.claude_manager.start()
            logger.info("Claude管理スレッドを非同期的に開始しました")
            
        except Exception as e:
            logger.error(f"Claude管理スレッド起動エラー: {e}")
            raise
    
    def _connect_to_master(self):
        """
        タスク管理マスターとのTCP接続を確立（ドキュメント: 18行目）
        """
        try:
            self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            
            # ソケットオプションの設定（バッファリング最適化）
            self.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)  # Nagleアルゴリズム無効化
            self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)  # KeepAlive有効化
            
            logger.debug(f"ソケット作成完了（TCP_NODELAY, SO_KEEPALIVE有効）")
            
            self.socket.connect((self.host, self.port))
            logger.debug(f"マスターに接続完了: {self.host}:{self.port}")
            
            # ワーカーIDを送信元ポート番号から生成（ドキュメント: 20行目）
            local_addr = self.socket.getsockname()
            remote_addr = self.socket.getpeername()
            local_port = local_addr[1]
            self.worker_id = str(local_port)
            
            # Claude管理スレッドにワーカーIDを設定
            if self.claude_manager:
                self.claude_manager.worker_id = self.worker_id
                logger.debug(f"Claude管理スレッドにワーカーIDを設定: {self.worker_id}")
            
            logger.info(f"タスク管理マスターに接続 (ワーカーID: {self.worker_id})")
            logger.debug(f"ローカルアドレス: {local_addr}, リモートアドレス: {remote_addr}")
            
        except Exception as e:
            logger.error(f"タスク管理マスター接続エラー: {e}")
            raise
    
    def _join_master(self):
        """
        参入処理（ドキュメント: 18行目）
        """
        # 参入メッセージ送信（ドキュメント: 20行目）
        join_message = {
            "type": "JOIN",
            "msg": self.worker_id
        }
        
        try:
            message_data = json.dumps(join_message) + '\n'
            logger.debug(f"送信する参入メッセージ: {repr(message_data)}")
            
            bytes_sent = self.socket.send(message_data.encode('utf-8'))
            logger.info(f"参入メッセージを送信しました ({bytes_sent}バイト)")
            
            # 参入応答メッセージ待ち（ドキュメント: 22行目）
            self.socket.settimeout(10.0)
            logger.debug("参入応答メッセージ待ち中...")
            
            response_data = self.socket.recv(1024).decode('utf-8')
            logger.debug(f"受信した参入応答: {repr(response_data)}")
            
            if response_data:
                response = json.loads(response_data.strip())
                logger.debug(f"解析した参入応答: {response}")
                
                if response.get("type") == "JOIN_ACK":
                    logger.info("参入応答メッセージを受信しました")
                    self.socket.settimeout(None)
                else:
                    raise Exception(f"無効な参入応答: {response}")
            else:
                raise Exception("参入応答メッセージが受信できませんでした")
                
        except Exception as e:
            logger.error(f"参入処理エラー: {e}")
            raise
    
    def _main_loop(self):
        """
        メインループ（ドキュメント: 26行目）
        継続的に以下のイベントを待つ（長時間のブロッキングが発生しないように工夫）:
        - タスク管理マスターからのヘルスチェック受信
        - タスク管理マスターからのタスク実行依頼受信
        - Claude管理スレッドからのタスク完了通知
        - タスク管理マスターからの切断通知の受信
        """
        logger.info("メインループを開始（ブロッキング回避設計）")
        loop_count = 0
        
        while self.running:
            try:
                loop_count += 1
                
                # 1000回ごとにハートビートログ
                if loop_count % 1000 == 0:
                    logger.debug(f"[LOOP] メインループ実行中 (回数: {loop_count})")
                    # ソケット状態の確認
                    try:
                        local_addr = self.socket.getsockname()
                        remote_addr = self.socket.getpeername()
                        logger.debug(f"[LOOP] ソケット状態OK: {local_addr} -> {remote_addr}")
                    except Exception as e:
                        logger.error(f"[LOOP] ソケット状態エラー: {e}")
                    
                    # 実行中タスクの確認
                    active_threads = threading.active_count()
                    task_threads = [t for t in threading.enumerate() if t.name.startswith('task-')]
                    logger.debug(f"[LOOP] アクティブスレッド数: {active_threads}, タスクスレッド数: {len(task_threads)}")
                    if task_threads:
                        logger.debug(f"[LOOP] 実行中タスク: {[t.name for t in task_threads]}")
                
                # タスク管理マスターからのメッセージ受信（ノンブロッキング）
                self._receive_master_messages()
                
                # REQUESTメッセージ受信後は受信頻度を上げる
                current_time = time.time()
                if self.last_request_time:
                    time_since_request = current_time - self.last_request_time
                    if time_since_request < 10.0:  # REQUEST後10秒間は高頻度チェック
                        # 追加の受信チェック
                        self._receive_master_messages()
                
                # Claude管理スレッドからの結果処理（ノンブロッキング）
                self._process_claude_results()
                
                # 短い間隔でポーリング（長時間ブロッキング防止：ドキュメント26行目）
                if self.last_request_time and current_time - self.last_request_time < 5.0:
                    time.sleep(0.001)  # REQUEST後は超高頻度（1ミリ秒）
                else:
                    time.sleep(0.05)  # 通常頻度（50ミリ秒）
                
            except Exception as e:
                if self.running:
                    logger.error(f"メインループエラー: {e}")
                    logger.error(traceback.format_exc())
                    break
        
        logger.info("メインループを終了")
    
    def _receive_master_messages(self):
        """
        タスク管理マスターからのメッセージ受信
        複数のメッセージをまとめて受信する場合があることを考慮（ドキュメント: 33行目）
        """
        try:
            # selectを使用してより確実な受信処理
            import select
            # REQUESTメッセージ受信後は短いタイムアウトで高頻度チェック
            current_time = time.time()
            if self.last_request_time and current_time - self.last_request_time < 5.0:
                # REQUEST後5秒間は短いタイムアウト（高頻度チェック）
                timeout = 0.001
            else:
                # 通常は長めのタイムアウト
                timeout = 0.1
            ready = select.select([self.socket], [], [], timeout)
            
            if ready[0]:
                # データが利用可能
                raw_data = self.socket.recv(65536)  # バッファサイズを増やして確実に受信
                if raw_data:
                    data = raw_data.decode('utf-8', errors='replace')
                    self._recv_buffer += data
                    logger.debug(f"[RECV] 現在のバッファ長: {len(self._recv_buffer)}")
                    
                    # 改行で区切られた複数のJSONメッセージを処理
                    message_count = 0
                    while '\n' in self._recv_buffer:
                        line, self._recv_buffer = self._recv_buffer.split('\n', 1)
                        line = line.strip()
                        
                        if line:
                            message_count += 1
                            try:
                                message = json.loads(line)
                                logger.info(f"[RECV] 受信メッセージ#{message_count}: {message}")
                                self._handle_master_message(message)
                            except json.JSONDecodeError as e:
                                logger.error(f"[RECV] JSON解析エラー: {e}")
                                logger.error(f"[RECV] 受信データ行: {repr(line)}")
                    
                    if message_count > 0:
                        logger.info(f"[RECV] 処理したメッセージ数: {message_count}")
                        
                elif raw_data == b'':
                    # 接続切断（recv()が空バイトを返す）
                    logger.warning("[RECV] タスク管理マスターとの接続が切断されました（空データ受信）")
                    self.running = False
            else:
                # データなし（正常）
                pass
                
        except ConnectionResetError:
            logger.warning("[RECV] タスク管理マスターとの接続がリセットされました")
            self.running = False
        except Exception as e:
            if self.running:
                logger.error(f"[RECV] マスターメッセージ受信エラー: {e}")
                logger.error(traceback.format_exc())
                self.running = False
    
    def _handle_master_message(self, message: Dict[str, Any]):
        """
        タスク管理マスターからのメッセージを処理
        
        Args:
            message: 受信したメッセージ
        """
        msg_type = message.get("type")
        req_id = message.get("req_id", "N/A")
        
        logger.info(f"[HANDLE] メッセージ処理開始: type={msg_type}, req_id={req_id}")
        
        if msg_type == "CHECK":
            # ヘルスチェック（ドキュメント: 37行目）
            current_time = time.time()
            if self.last_request_time:
                time_since_request = current_time - self.last_request_time
                logger.info(f"[HANDLE] ヘルスチェック処理開始 (req_id: {req_id}, REQUEST後: {time_since_request:.3f}秒)")
            else:
                logger.info(f"[HANDLE] ヘルスチェック処理開始 (req_id: {req_id})")
            self._handle_health_check(message)
            logger.info(f"[HANDLE] ヘルスチェック処理完了 (req_id: {req_id})")
        
        elif msg_type == "REQUEST":
            # タスク実行依頼（ドキュメント: 39行目）
            self.last_request_time = time.time()
            self.request_count += 1
            logger.info(f"[HANDLE] タスク実行依頼受信 (req_id: {req_id}, count: {self.request_count}, time: {self.last_request_time})")
            self._handle_task_request(message)
            logger.info(f"[HANDLE] タスク実行依頼をClaude管理スレッドに転送 (req_id: {req_id})")
        
        elif msg_type == "DISCONNECT":
            # 切断通知（ドキュメント: 51行目）
            logger.info("[HANDLE] 切断通知を受信しました")
            self.running = False
        
        else:
            logger.warning(f"[HANDLE] 未知のメッセージタイプ: {msg_type}")
    
    def _handle_health_check(self, message: Dict[str, Any]):
        """
        ヘルスチェック処理（ドキュメント: 37行目）
        即時にtype=CHECK_ACKを返答
        
        Args:
            message: ヘルスチェックメッセージ
        """
        req_id = message.get("req_id")
        logger.info(f"[CHECK] ヘルスチェック受信 (req_id: {req_id})")
        
        # 即時応答（ドキュメント: 37行目）
        check_ack = {
            "type": "CHECK_ACK",
            "msg": "",
            "req_id": req_id
        }
        
        try:
            message_data = json.dumps(check_ack) + '\n'
            logger.debug(f"[CHECK] 送信するヘルスチェック応答: {repr(message_data)}")
            
            bytes_sent = self.socket.send(message_data.encode('utf-8'))
            
            # 即座に送信バッファをフラッシュ
            try:
                self.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            except:
                pass
                
            logger.info(f"[CHECK] ヘルスチェック応答送信完了 (req_id: {req_id}, {bytes_sent}バイト)")
            
            # 送信直後のソケット状態確認
            try:
                local_addr = self.socket.getsockname()
                remote_addr = self.socket.getpeername()
                logger.debug(f"[CHECK] 送信後ソケット状態: {local_addr} -> {remote_addr}")
            except Exception as sock_e:
                logger.error(f"[CHECK] 送信後ソケット状態エラー: {sock_e}")
                
        except Exception as e:
            logger.error(f"[CHECK] ヘルスチェック応答エラー: {e}")
            logger.error(traceback.format_exc())
    
    def _handle_task_request(self, message: Dict[str, Any]):
        """
        タスク実行依頼処理（ドキュメント: 39行目）
        まず即時にタスク実行依頼承諾メッセージを返答し、
        その後、Claude管理スレッドに渡す
        
        Args:
            message: タスク実行依頼メッセージ
        """
        req_id = message.get("req_id")
        prompt_text = message.get("msg", "")
        
        logger.info(f"[REQUEST] タスク実行依頼を受信 (req_id: {req_id})")
        
        # 即時に承諾応答（ドキュメント: 39行目）
        request_ack = {
            "type": "REQUEST_ACK",
            "msg": "",
            "req_id": req_id
        }
        
        try:
            message_data = json.dumps(request_ack) + '\n'
            bytes_sent = self.socket.send(message_data.encode('utf-8'))
            
            # 即座に送信バッファをフラッシュ
            try:
                self.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            except:
                pass
                
            logger.info(f"[REQUEST] タスク実行依頼承諾送信完了 (req_id: {req_id}, {bytes_sent}バイト)")
            logger.debug(f"[REQUEST] 送信した承諾メッセージ: {repr(message_data)}")
        except Exception as e:
            logger.error(f"[REQUEST] タスク実行依頼承諾エラー: {e}")
            return
        
        # Claude管理スレッドにタスク実行依頼を渡す（ドキュメント: 39行目）
        if self.claude_manager:
            task_filename = f"task_{req_id}"
            self.claude_manager.add_message({
                "type": "REQUEST",
                "msg": prompt_text,
                "req_id": req_id,
                "task_filename": task_filename
            })
            logger.info(f"タスク実行依頼をClaude管理スレッドに送信 (req_id: {req_id})")
            logger.debug(f"メインループは継続してヘルスチェックを受信可能")
            logger.info(f"[HANDLE] REQUEST_ACK送信完了、タスクは非同期実行中 (req_id: {req_id})")
    
    def _handle_claude_result(self, result: Dict[str, Any]):
        """
        Claude管理スレッドからの結果を処理
        
        Args:
            result: Claude管理スレッドからの結果
        """
        with self.result_lock:
            self.result_queue.append(result)
    
    def _process_claude_results(self):
        """
        Claude管理スレッドからのタスク完了通知を処理（ドキュメント: 45行目）
        """
        with self.result_lock:
            results = self.result_queue.copy()
            self.result_queue.clear()
        
        for result in results:
            self._send_task_result(result)
    
    def _send_task_result(self, result: Dict[str, Any]):
        """
        タスク結果報告をマスターに送信（ドキュメント: 45行目）
        
        Args:
            result: タスク結果
        """
        try:
            result_type = result.get("type")
            task_filename = result.get("msg", "")
            req_id = result.get("req_id", "unknown")
            
            # タスク結果報告メッセージ（ドキュメント: 47行目）
            if result_type == "USAGE_LIMITED":
                # USAGE_LIMITEDの場合はワーカーIDを送信
                result_message = {
                    "type": "USAGE_LIMITED",
                    "msg": self.worker_id
                }
            else:
                # DONE/FAILEDの場合はタスクファイル名を送信
                result_message = {
                    "type": result_type,
                    "msg": task_filename
                }
            
            message_data = json.dumps(result_message) + '\n'
            self.socket.send(message_data.encode('utf-8'))
            
            logger.info(f"[RESULT] タスク結果報告をマスターに送信: {result_type} (req_id: {req_id}, task: {task_filename})")
            
        except Exception as e:
            logger.error(f"タスク結果報告エラー: {e}")
    
    def _cleanup(self):
        """
        ワーカー終了処理（ドキュメント: 51行目）
        """
        logger.info("ワーカー終了処理を開始")
        
        # Claude管理スレッドを終了（ドキュメント: 53行目）
        if self.claude_manager:
            self.claude_manager.stop()
        
        # タスク管理マスターとの接続を切断（ドキュメント: 53行目）
        if self.socket:
            try:
                # 離脱メッセージ送信
                leave_message = {"type": "LEAVE", "msg": ""}
                message_data = json.dumps(leave_message) + '\n'
                self.socket.send(message_data.encode('utf-8'))
                
                self.socket.close()
                logger.info("タスク管理マスターとの接続を切断しました")
            except Exception as e:
                logger.error(f"接続切断エラー: {e}")
        
        logger.info("ワーカー終了処理を完了しました")


def main():
    """
    メイン関数
    引数の処理とタスクワーカーの起動
    """
    parser = argparse.ArgumentParser(description='タスクワーカー')
    
    # 位置引数（ドキュメント: 5行目）
    parser.add_argument(
        'host',
        nargs='?',
        default='localhost',
        help='タスク管理マスターのホスト名 (デフォルト: localhost)'
    )
    parser.add_argument(
        'port',
        type=int,
        nargs='?',
        default=34567,
        help='タスク管理マスターのポート番号 (デフォルト: 34567)'
    )
    
    # オプション引数（ドキュメント: 7行目）
    parser.add_argument(
        '--root-dir',
        type=str,
        help='ルートディレクトリパス (デフォルト: カレントディレクトリ)'
    )
    
    # Claudeモデル指定（ドキュメント: 9行目）
    parser.add_argument(
        '--opus',
        action='store_true',
        help='Opusモデルを使用する (デフォルト: Sonnet)'
    )
    
    # デバッグオプション
    parser.add_argument(
        '--debug-auth',
        action='store_true',
        help='Claude認証状態のデバッグ情報を表示'
    )
    parser.add_argument(
        '--skip-auth-check',
        action='store_true',
        help='Claude認証チェックをスキップ（デバッグ用）'
    )
    parser.add_argument(
        '--debug',
        action='store_true',
        help='デバッグログを有効にする'
    )
    
    args = parser.parse_args()
    
    # デバッグモードでログレベルを変更
    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)
        logger.info("デバッグモードを有効にしました")
    
    try:
        # タスクワーカーを起動
        worker = TaskWorker(
            host=args.host,
            port=args.port,
            root_dir=args.root_dir,
            use_opus=args.opus,
            debug_auth=args.debug_auth,
            skip_auth_check=args.skip_auth_check
        )
        worker.start()
        
    except KeyboardInterrupt:
        logger.info("Ctrl-Cで終了")
    except Exception as e:
        logger.error(f"予期しないエラー: {e}")
        logger.error(traceback.format_exc())
        sys.exit(1)


if __name__ == "__main__":
    main()