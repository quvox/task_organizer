# Task Organizer

AIエージェントによる業務の並行実行システム

## 概要

Task Organizerは、与えられた業務を並行実行可能なタスクに分割し、AIエージェント（Claude）に並行実行させるためのツールです。AIエージェントのコンテキストをタスクごとにリセットすることでコストを圧縮し、複数のAIエージェントで並行実行することで業務完了までの時間を短縮します。

## 特徴

- 🔄 **並行実行**: 複数のAIエージェントによるタスクの同時実行
- 💰 **コスト最適化**: タスクごとのコンテキストリセットによるコスト圧縮
- ⏱️ **時間短縮**: 分散処理による業務完了時間の短縮
- 🏗️ **スケーラブル**: 必要に応じてワーカーを追加可能
- 📊 **モニタリング**: タスクの進行状況とワーカー状態の監視

## クイックスタート

### 1. 自動セットアップ（推奨）

```bash
# リポジトリのクローン
git clone <repository-url>
cd task_organizer

# 開発環境の自動セットアップ
./scripts/setup-dev.sh

# デモ実行
./scripts/demo.sh
```

### 2. 手動セットアップ

```bash
# ビルド
make build

# サンプル実行
make run-sample
```

## Makeコマンド一覧

基本的な操作は`make`コマンドで実行できます：

### ビルドとインストール
```bash
make build          # バイナリをビルド
make build-all      # 全プラットフォーム向けビルド
make install        # システムにインストール（/usr/local/bin）
make install-user   # ユーザーディレクトリにインストール（~/bin）
make uninstall      # システムからアンインストール
```

### 開発
```bash
make dev            # 開発モード（ファイル変更時の自動ビルド）
make fmt            # コードフォーマット
make lint           # 静的解析
make test           # テスト実行
make test-coverage  # テストカバレッジ測定
```

### 実行とデモ
```bash
make run-sample     # サンプル実行（デモ）
make setup-sample   # サンプルファイルの作成
```

### Docker
```bash
make docker         # Dockerイメージのビルド
docker-compose up   # Docker Composeでの実行
```

### その他
```bash
make clean          # ビルド成果物の削除
make clean-all      # 全ファイルのクリーンアップ
make release        # リリース用パッケージの作成
make help           # 全コマンドのヘルプ表示
```

## 使用方法

### 基本的なワークフロー

1. **タスクファイルの生成**
```bash
./build/taskorganizer create prompt.txt targets.txt --root ./workspace
```

2. **タスク管理マスタの起動**
```bash
./build/taskorganizer master --root ./workspace
```

3. **タスクワーカーの起動**（別ターミナル）
```bash
./build/taskorganizer worker --root ./workspace
```

### コマンドオプション

#### create コマンド
```bash
taskorganizer create <prompt_file> <target_file> [options]

オプション:
  --root <dir>    作業ディレクトリ（デフォルト: カレントディレクトリ）
  --clear         既存の.tasksディレクトリを削除
```

#### master コマンド
```bash
taskorganizer master [port] [options]

引数:
  port            待受ポート番号（デフォルト: 34567）

オプション:
  --root <dir>    作業ディレクトリ
```

#### worker コマンド
```bash
taskorganizer worker [host:port] [options]

引数:
  host:port       マスタのアドレス（デフォルト: localhost:34567）

オプション:
  --root <dir>    作業ディレクトリ
  --opus          Opusモデルを使用（デフォルト: Sonnet）
```

## Docker での実行

### 基本的なDocker実行
```bash
# イメージのビルド
make docker

# マスタの起動
docker run -p 34567:34567 -v $(pwd)/workspace:/workspace taskorganizer master --root /workspace

# ワーカーの起動
docker run -v $(pwd)/workspace:/workspace taskorganizer worker master:34567 --root /workspace
```

### Docker Compose での実行
```bash
# サービス全体の起動
docker-compose up --build

# タスク生成のみ実行
docker-compose run --rm creator

# スケール実行（ワーカーを増やす）
docker-compose up --scale worker1=3 --scale worker2=2
```

## ディレクトリ構造

```
task_organizer/
├── Makefile                 # ビルド自動化
├── Dockerfile              # Dockerイメージ定義
├── docker-compose.yml      # Docker Compose設定
├── README.md               # このファイル
├── scripts/                # 各種スクリプト
│   ├── setup-dev.sh       # 開発環境セットアップ
│   └── demo.sh            # デモ実行
├── src/                    # ソースコード
│   ├── main.go            # メインエントリーポイント
│   ├── messages.go        # メッセージフォーマット
│   ├── create.go          # タスク生成ツール
│   ├── master.go          # タスク管理マスタ
│   ├── worker.go          # タスクワーカー
│   ├── go.mod             # Go モジュール
│   └── README.md          # 詳細ドキュメント
├── build/                  # ビルド成果物
├── sample/                 # サンプルファイル
└── workspace/              # 作業ディレクトリ
    └── .tasks/            # タスク管理ディレクトリ
        ├── pending/       # 実行待ちタスク
        ├── working/       # 実行中タスク
        ├── done/          # 完了タスク
        └── failed/        # 失敗タスク
```

## 開発

### 開発環境のセットアップ
```bash
# 自動セットアップ
./scripts/setup-dev.sh

# 手動セットアップ
make deps          # 依存関係の取得
make build         # ビルド
make test          # テスト実行
```

### 開発モード
```bash
# ファイル変更時の自動ビルド
make dev

# VS Code での開発
code .
```

### コードの品質チェック
```bash
make fmt           # フォーマット
make lint          # 静的解析
make security      # セキュリティチェック
make test-coverage # テストカバレッジ
```

## 依存関係

### 実行環境
- Go 1.20以上
- Claude CLI（AI実行用）

### 開発環境
- golangci-lint（静的解析）
- gosec（セキュリティ解析）
- Docker（コンテナ実行）
- Docker Compose（マルチコンテナ管理）

### Go モジュール
- github.com/google/uuid（UUID生成）

## トラブルシューティング

### よくある問題

1. **ビルドエラー**
```bash
# 依存関係の再取得
make deps
make clean
make build
```

2. **接続エラー**
```bash
# ポートの確認
netstat -an | grep 34567

# ファイアウォールの確認
# 必要に応じてポート34567を開放
```

3. **Claude CLIエラー**
```bash
# Claude CLIのインストール確認
claude --version

# 認証状態の確認
claude auth status
```

## サンプル実行例

URLリストから各ページの要約を作成する例：

```bash
# 1. サンプルファイルの確認
cat sample/prompt.txt
# URLリストのURLに一つずつアクセスして、ページの要約をresults/ディレクトリにマークダウン形式でファイル出力しなさい

cat sample/targets.txt
# https://example.com
# https://github.com
# https://httpbin.org/get

# 2. 全工程の自動実行
make run-sample

# 3. または段階的実行
./build/taskorganizer create sample/prompt.txt sample/targets.txt --clear
./build/taskorganizer master &    # バックグラウンドで起動
./build/taskorganizer worker &    # バックグラウンドで起動
```

## ライセンス

MIT License

## 貢献

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## サポート

- Issue: GitHub Issues を使用
- ドキュメント: `src/README.md` を参照
- デモ: `./scripts/demo.sh` を実行
