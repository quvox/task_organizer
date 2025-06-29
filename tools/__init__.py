"""
タスク管理ツールパッケージ

このパッケージは、タスクの配布、実行、管理を行うツール群を提供します。

主要モジュール:
- task_master: タスク管理マスタ - 複数のワーカーを管理しタスクを配布
- task_worker: タスクワーカー - タスクマスタからタスクを受信してClaude実行
- task_creator: タスククリエーター - .tasksディレクトリにタスクファイルを作成

使用例:
    from tools import TaskMaster, TaskWorker
    
    # タスクマスタ起動
    master = TaskMaster()
    master.start()
    
    # タスクワーカー起動
    worker = TaskWorker()
    worker.start()
"""

__version__ = "0.1.0"
__author__ = "Claude Code"
__email__ = "noreply@anthropic.com"

# 主要クラスをパッケージレベルでインポート可能にする
from .task_master import TaskMaster
from .task_worker import TaskWorker

# __all__でエクスポートするシンボルを明示的に定義
__all__ = [
    "TaskMaster",
    "TaskWorker",
    "__version__",
    "__author__",
    "__email__"
]