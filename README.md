# タスク管理ツール (Task Management Tools)

分散タスク実行システム - Claude APIを使用したタスク管理ツール群

## 概要

このパッケージは、複数のワーカーがタスクを並列実行する分散システムを提供します。タスクマスタがタスクを管理し、複数のタスクワーカーがClaude APIを使用してタスクを実行します。

## 主要コンポーネント

### 1. タスクマスタ (task_master.py)
- 複数のタスクワーカーとの接続を管理
- `.tasks`ディレクトリからタスクを読み込み、ワーカーに配布
- ヘルスチェック機能によるワーカーの死活監視
- タスクの実行状況追跡

### 2. タスクワーカー (task_worker.py)
- タスクマスタからタスクを受信
- Claude APIを使用してタスクを実行
- リアルタイムでの実行ログ出力
- ヘルスチェック応答

### 3. タスククリエーター (task_creator.py)
- `.tasks`ディレクトリへのタスクファイル作成
- タスクテンプレートの生成

## インストール

### GitHubから直接インストール
```bash
# 最新版をインストール
pip install git+https://github.com/quvox/task_organizer.git

# 特定のブランチまたはタグをインストール
pip install git+https://github.com/quvox/task_organizer.git@branch-name
pip install git+https://github.com/quvox/task_organizer.git@v1.0.0

# 開発依存関係を含むインストール
pip install "git+https://github.com/quvox/task_organizer.git[dev]"
```

### ローカル開発時のインストール
```bash
# リポジトリをクローン
git clone https://github.com/quvox/task_organizer.git
cd your-repo-name

# 開発モードでインストール
pip install -e .

# 開発依存関係を含むインストール
pip install -e .[dev]
```

### venv環境での推奨インストール方法
```bash
# 仮想環境を作成
python -m venv venv
source venv/bin/activate  # Linux/Mac
# または venv\Scripts\activate  # Windows

# GitHubから直接インストール
pip install git+https://github.com/quvox/task_organizer.git
```

## 使用方法

### 1. パッケージとしての使用

```python
from tools import TaskMaster, TaskWorker

# タスクマスタ起動
master = TaskMaster(port=34567, tasks_dir=".tasks")
master.start()

# タスクワーカー起動
worker = TaskWorker(host="localhost", port=34567)
worker.start()
```

### 2. コマンドラインツールとしての使用

インストール後、以下のコマンドが利用可能になります。

事前に、作業場所のディレクトリに移動して、claude CLIでログインしておいてください。（ツール内ではログインできません）


```bash
# タスクマスタ起動
task-master [ポート番号] [--tasks-dir タスクディレクトリ]

# タスクワーカー起動
task-worker [ホスト名] [ポート番号] [--root-dir ルートディレクトリ]

# タスク作成
task-creator [タスクファイル名] [プロンプトテキストファイル名]
```

### 例

```bash
# タスクマスタをポート34567で起動
task-master 34567 --tasks-dir .tasks

# ワーカーを localhost:34567 に接続
task-worker localhost 34567

# Dockerコンテナ内からホストのマスタに接続
task-worker host.docker.internal 34567
```

## ディレクトリ構造

```
project/
├── .tasks/           # タスクファイル置き場
│   ├── pending/      # 実行待ちタスク
│   ├── working/      # 実行中タスク
│   └── completed/    # 完了タスク
├── tools/            # パッケージソース
│   ├── __init__.py
│   ├── task_master.py
│   ├── task_worker.py
│   └── task_creator.py
├── setup.py
├── pyproject.toml
└── README.md
```

## 機能詳細

### メッセージ通信
- JSON形式でのTCP通信
- リクエスト/レスポンス形式
- タイムアウト監視

### タスク実行
- Claude APIによるタスク実行
- ストリーミング出力のリアルタイム表示
- エラーハンドリングと再試行

### 監視機能
- ワーカーのヘルスチェック
- タスク実行タイムアウト
- 異常終了時の自動クリーンアップ

## 開発

### 開発環境セットアップ
```bash
# 仮想環境作成
python -m venv venv
source venv/bin/activate  # Linux/Mac
# または
venv\Scripts\activate     # Windows

# 開発モードでインストール
pip install -e .[dev]
```

### テスト実行
```bash
pytest
```

### コードフォーマット
```bash
black tools/
isort tools/
```

### 型チェック
```bash
mypy tools/
```

## 要件

- Python 3.8以上
- Claude Code CLI (claude コマンド)
- 標準ライブラリのみ使用（外部依存なし）


## ライセンス

MIT License


## 作者

Quvox
