# Task Organizer テストサンプル

このディレクトリには、Task Organizerのテスト用サンプルファイルが含まれています。

## サンプルファイル

### 1. Web要約タスク
- **業務プロンプト**: `business_prompt.txt`
- **ターゲットリスト**: `target_list.txt`
- **内容**: 指定されたURLのWebページを要約するタスク

### 2. ファイル分析タスク
- **業務プロンプト**: `business_prompt_files.txt`
- **ターゲットリスト**: `target_files.txt`
- **内容**: 指定されたソースコードファイルを分析するタスク

### 3. 数値計算タスク
- **業務プロンプト**: `business_prompt_simple.txt`
- **ターゲットリスト**: `target_numbers.txt`
- **内容**: 指定された数値に対して計算を実行するタスク

## 使用方法

### 基本的な実行手順

1. **タスクの生成**
   ```bash
   # Web要約タスクの場合
   ../build/taskorganizer create business_prompt.txt target_list.txt --clear
   
   # ファイル分析タスクの場合
   ../build/taskorganizer create business_prompt_files.txt target_files.txt --clear
   
   # 数値計算タスクの場合
   ../build/taskorganizer create business_prompt_simple.txt target_numbers.txt --clear
   ```

2. **タスク管理マスタの起動**
   ```bash
   ../build/taskorganizer master
   ```

3. **タスクワーカーの起動**（別ターミナルで）
   ```bash
   # ワーカー1
   ../build/taskorganizer worker
   
   # ワーカー2（並列実行したい場合）
   ../build/taskorganizer worker
   ```

### 自動テストスクリプト

`test_task_organizer.sh`を実行すると、Web要約タスクを自動的に実行します：

```bash
./test_task_organizer.sh
```

## オプション

### タスク生成時のオプション
- `--root-dir <path>`: ルートディレクトリを指定
- `--clear`: 既存の.tasksディレクトリを削除してから開始

### マスター起動時のオプション
- ポート番号を指定（デフォルト: 34567）
- `--root-dir <path>`: ルートディレクトリを指定

### ワーカー起動時のオプション
- ホスト名とポート番号を指定（デフォルト: localhost 34567）
- `--root-dir <path>`: ルートディレクトリを指定
- `--opus`: Claude Opus 4モデルを使用（デフォルトはSonnet 4）

## 結果の確認

タスク実行後、以下のディレクトリで結果を確認できます：

- `.tasks/done/`: 完了したタスクファイル
- `.tasks/failed/`: 失敗したタスクファイル
- `results/`: Web要約の結果
- `analysis/`: ファイル分析の結果
- `calculations/`: 数値計算の結果

## 注意事項

- Claude CLIがインストールされ、認証が完了している必要があります
- タスクワーカーはルートディレクトリ（sampleディレクトリ）で実行されます
- 結果を保存するディレクトリ（results/, analysis/, calculations/）は自動的に作成されます