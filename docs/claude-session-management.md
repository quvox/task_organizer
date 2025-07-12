# Claude セッション管理

Task OrganizerでのClaude CLI セッション管理について説明します。

## セッションIDの取得方法

Claude CLIでは`--output-format json`オプションを使用することで、レスポンスにセッションIDが含まれます。

### 基本的な使用例

```bash
# JSONフォーマットでレスポンスを取得
claude --output-format json -p "テストメッセージ"
```

### レスポンス例

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "duration_ms": 2971,
  "duration_api_ms": 5100,
  "num_turns": 1,
  "result": "こんにちは！どのようにお手伝いできますか？",
  "session_id": "13e88cd3-df55-4673-8894-a6d2bcb453f8",
  "total_cost_usd": 0.0189431,
  "usage": {
    "input_tokens": 4,
    "cache_creation_input_tokens": 3646,
    "cache_read_input_tokens": 10164,
    "output_tokens": 25,
    "service_tier": "standard"
  }
}
```

## セッションの再開

取得したセッションIDを使用して、後続のリクエストで同じセッションを継続できます。

```bash
# セッションIDを使用してセッションを再開
claude -output-format json -r "13e88cd3-df55-4673-8894-a6d2bcb453f8" -p "続きのメッセージ"
```

## Task Organizerでの実装

### 1. セッションID取得 (worker.go)

```go
// 初期化時にセッションIDを取得
func (w *TaskWorker) createClaudeSession() (string, error) {
    var cmdArgs []string
    if w.config.Opus {
        cmdArgs = []string{"claude", "--output-format", "json", "--model", "opus", "-p", "."}
    } else {
        cmdArgs = []string{"claude", "--output-format", "json", "--model", "sonnet", "-p", "."}
    }

    cmd := exec.CommandContext(w.ctx, cmdArgs[0], cmdArgs[1:]...)
    output, err := cmd.Output()
    if err != nil {
        return "", fmt.Errorf("Claude CLI failed: %w", err)
    }

    // JSONレスポンスからセッションIDを抽出
    var response struct {
        SessionID string `json:"session_id"`
        IsError   bool   `json:"is_error"`
    }

    if err := json.Unmarshal(output, &response); err != nil {
        return "", fmt.Errorf("failed to parse Claude response: %w", err)
    }

    return response.SessionID, nil
}
```

### 2. セッションを使用したタスク実行

```go
// 取得したセッションIDでタスクを実行
func (w *TaskWorker) executeTask(promptText string) {
    cmdArgs := []string{
        "claude", "--output-format", "json", 
        "-r", w.sessionID,  // セッションIDを指定
        "--allowedTools", "WebFetch,Read,Write,Bash", 
        "--model", "sonnet", 
        "-p", promptText,
    }
    
    // ... 実行処理
}
```

## メリット

1. **コンテキスト継続**: 同一セッション内で前の会話を覚えている
2. **効率性**: セッション初期化のオーバーヘッドを削減
3. **一貫性**: 複数のタスク間で一貫した応答を得られる

## 注意点

1. **セッション有効期限**: Claudeセッションには有効期限があるため、長時間の運用では再作成が必要
2. **エラーハンドリング**: セッションが無効になった場合の処理を実装する必要がある
3. **並行実行**: 複数ワーカーでは個別のセッションIDが必要

## トラブルシューティング

### セッションIDが取得できない場合

```bash
# Claude CLIの認証状態を確認
claude auth status

# ヘルプでオプションを確認
claude --help
```

### セッションが無効になった場合

```go
// エラー時は新しいセッションを作成
if strings.Contains(err.Error(), "session") {
    newSessionID, err := w.createClaudeSession()
    if err == nil {
        w.sessionID = newSessionID
        // タスクを再実行
    }
}
```

## 実際の使用例

```bash
# 1. 初期セッション作成
SESSION_ID=$(claude --output-format json -p "." | jq -r '.session_id')

# 2. セッションを使用した実行
claude --output-format json -r "$SESSION_ID" -p "タスク1を実行"
claude --output-format json -r "$SESSION_ID" -p "タスク2を実行"
```

これにより、Task Organizerは効率的にClaude セッションを管理し、コンテキストを保持しながらタスクを実行できます。