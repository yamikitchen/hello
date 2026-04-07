package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Task はタスク1件分のデータを表す構造体です。
// json:"..." はAPIのレスポンスに使うJSONのキー名を指定しています。
type Task struct {
	ID        int       `json:"id"`         // タスクを一意に識別する番号
	Title     string    `json:"title"`      // タスクの内容
	Done      bool      `json:"done"`       // 完了済みかどうか（true=完了）
	CreatedAt time.Time `json:"created_at"` // 作成日時
}

// TaskStore はタスクをメモリ上に保存する入れ物です。
// 複数のリクエストが同時に来ても壊れないよう、mutex（排他ロック）を使います。
type TaskStore struct {
	mutex  sync.Mutex // 同時アクセスを防ぐためのロック
	tasks  []Task     // タスクの一覧
	nextID int        // 次に追加するタスクに割り振るID
}

// NewTaskStore は TaskStore を初期化して返す関数です。
// IDは1から始めます。
func NewTaskStore() *TaskStore {
	return &TaskStore{nextID: 1}
}

// GetAll は保存されているタスクを全件返します。
// コピーを返すことで、呼び出し元から元データが書き換えられるのを防ぎます。
func (store *TaskStore) GetAll() []Task {
	store.mutex.Lock()
	defer store.mutex.Unlock() // 関数を抜けるときに自動でロック解除

	// スライスのコピーを作成して返す
	copied := make([]Task, len(store.tasks))
	copy(copied, store.tasks)
	return copied
}

// Add は新しいタスクを追加して、追加したタスクを返します。
func (store *TaskStore) Add(title string) Task {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	newTask := Task{
		ID:        store.nextID,
		Title:     title,
		Done:      false,
		CreatedAt: time.Now(),
	}
	store.nextID++
	store.tasks = append(store.tasks, newTask)
	return newTask
}

// Toggle は指定IDのタスクの完了状態を切り替えます（完了↔未完了）。
// タスクが見つかった場合は (更新後のタスク, true)、見つからない場合は (空, false) を返します。
func (store *TaskStore) Toggle(id int) (Task, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	for index, task := range store.tasks {
		if task.ID == id {
			store.tasks[index].Done = !store.tasks[index].Done // true↔falseを反転
			return store.tasks[index], true
		}
	}
	return Task{}, false // 見つからなかった
}

// Delete は指定IDのタスクを削除します。
// 削除できた場合は true、見つからなかった場合は false を返します。
func (store *TaskStore) Delete(id int) bool {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	for index, task := range store.tasks {
		if task.ID == id {
			// index番目の要素を除いた新しいスライスを作成
			store.tasks = append(store.tasks[:index], store.tasks[index+1:]...)
			return true
		}
	}
	return false // 見つからなかった
}

const maxTitleLength = 200 // タイトルの最大文字数

// sendJSON はHTTPレスポンスとしてJSONを返すヘルパー関数です。
func sendJSON(responseWriter http.ResponseWriter, statusCode int, data any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	if err := json.NewEncoder(responseWriter).Encode(data); err != nil {
		log.Printf("JSONエンコードエラー: %v", err)
	}
}

// tasksHandler は /api/tasks へのリクエストを処理します。
// GET  → タスク一覧を返す
// POST → 新しいタスクを追加する
func tasksHandler(store *TaskStore) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.Method {

		case http.MethodGet:
			// タスク一覧をJSONで返す
			sendJSON(responseWriter, http.StatusOK, store.GetAll())

		case http.MethodPost:
			// リクエストボディから title を取り出す
			var requestBody struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil || requestBody.Title == "" {
				http.Error(responseWriter, "タイトルが不正です", http.StatusBadRequest)
				return
			}
			if len([]rune(requestBody.Title)) > maxTitleLength {
				http.Error(responseWriter, "タイトルが長すぎます", http.StatusBadRequest)
				return
			}
			addedTask := store.Add(requestBody.Title)
			sendJSON(responseWriter, http.StatusCreated, addedTask)

		default:
			http.Error(responseWriter, "許可されていないメソッドです", http.StatusMethodNotAllowed)
		}
	}
}

// taskHandler は /api/tasks/{id} へのリクエストを処理します。
// PATCH  → 完了状態を切り替える
// DELETE → タスクを削除する
func taskHandler(store *TaskStore) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		// URLパスから {id} 部分を取り出して数値に変換する
		idString := request.PathValue("id")
		taskID, err := strconv.Atoi(idString)
		if err != nil {
			http.Error(responseWriter, "IDが不正です", http.StatusBadRequest)
			return
		}

		switch request.Method {

		case http.MethodPatch:
			// 完了状態を切り替える
			updatedTask, found := store.Toggle(taskID)
			if !found {
				http.Error(responseWriter, "タスクが見つかりません", http.StatusNotFound)
				return
			}
			sendJSON(responseWriter, http.StatusOK, updatedTask)

		case http.MethodDelete:
			// タスクを削除する
			if !store.Delete(taskID) {
				http.Error(responseWriter, "タスクが見つかりません", http.StatusNotFound)
				return
			}
			responseWriter.WriteHeader(http.StatusNoContent) // 削除成功（返すデータなし）

		default:
			http.Error(responseWriter, "許可されていないメソッドです", http.StatusMethodNotAllowed)
		}
	}
}

// indexPage はブラウザに返すHTMLページのテンプレートです。
// template.Must はテンプレートの読み込みに失敗したらすぐにパニックで終了させます。
var indexPage = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>タスク管理</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: sans-serif; background: #f5f5f5; padding: 2rem; }
  h1 { text-align: center; margin-bottom: 1.5rem; color: #333; }
  .container { max-width: 600px; margin: 0 auto; }
  .input-row { display: flex; gap: 0.5rem; margin-bottom: 1.5rem; }
  .input-row input {
    flex: 1; padding: 0.6rem 1rem; border: 1px solid #ccc;
    border-radius: 6px; font-size: 1rem;
  }
  .input-row button {
    padding: 0.6rem 1.2rem; background: #4CAF50; color: white;
    border: none; border-radius: 6px; cursor: pointer; font-size: 1rem;
  }
  .input-row button:hover { background: #45a049; }
  .task-list { list-style: none; display: flex; flex-direction: column; gap: 0.5rem; }
  .task-item {
    display: flex; align-items: center; gap: 0.75rem;
    background: white; padding: 0.75rem 1rem;
    border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1);
  }
  .task-item.done .task-title { text-decoration: line-through; color: #aaa; }
  .task-item input[type=checkbox] { width: 18px; height: 18px; cursor: pointer; }
  .task-title { flex: 1; font-size: 1rem; }
  .delete-btn {
    background: none; border: none; color: #e57373;
    font-size: 1.2rem; cursor: pointer; padding: 0 0.25rem;
  }
  .delete-btn:hover { color: #c62828; }
  .empty { text-align: center; color: #aaa; padding: 2rem; }
  .stats { text-align: right; font-size: 0.85rem; color: #888; margin-bottom: 0.5rem; }
</style>
</head>
<body>
<div class="container">
  <h1>タスク管理</h1>
  <div class="input-row">
    <input id="newTask" type="text" placeholder="新しいタスクを入力..." />
    <button onclick="addTask()">追加</button>
  </div>
  <div class="stats" id="stats"></div>
  <ul class="task-list" id="taskList"></ul>
</div>
<script>
// サーバーからタスク一覧を取得して画面に表示する
async function loadTasks() {
  const response = await fetch('/api/tasks');
  const taskList = await response.json();
  renderTaskList(taskList || []);
}

// タスクの配列を受け取り、画面に一覧表示する
function renderTaskList(taskList) {
  const listElement = document.getElementById('taskList');
  const statsElement = document.getElementById('stats');

  // 完了済みタスクの件数を数える
  const completedCount = taskList.filter(task => task.done).length;
  statsElement.textContent = taskList.length > 0
    ? completedCount + ' / ' + taskList.length + ' 完了'
    : '';

  if (taskList.length === 0) {
    listElement.innerHTML = '<li class="empty">タスクがありません</li>';
    return;
  }

  // タスクごとにHTMLを生成してまとめて表示する
  listElement.innerHTML = taskList.map(task => ` + "`" + `
    <li class="task-item ${task.done ? 'done' : ''}" id="task-${task.id}">
      <input type="checkbox" ${task.done ? 'checked' : ''} onchange="toggleTask(${task.id})" />
      <span class="task-title">${escapeHtml(task.title)}</span>
      <button class="delete-btn" onclick="deleteTask(${task.id})" title="削除">&#x2715;</button>
    </li>` + "`" + `).join('');
}

// XSS対策: HTMLに埋め込む文字列の特殊文字をエスケープする
function escapeHtml(text) {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#x27;');
}

// 入力欄のタスクをサーバーに送信して追加する
async function addTask() {
  const inputElement = document.getElementById('newTask');
  const title = inputElement.value.trim(); // 前後の空白を除去
  if (!title) return; // 空欄なら何もしない

  await fetch('/api/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title })
  });

  inputElement.value = ''; // 入力欄をクリア
  loadTasks();             // 一覧を再読み込み
}

// 指定IDのタスクの完了状態を切り替える
async function toggleTask(taskID) {
  await fetch('/api/tasks/' + taskID, { method: 'PATCH' });
  loadTasks();
}

// 指定IDのタスクを削除する
async function deleteTask(taskID) {
  await fetch('/api/tasks/' + taskID, { method: 'DELETE' });
  loadTasks();
}

// Enterキーを押したときもタスクを追加できるようにする
document.getElementById('newTask').addEventListener('keydown', function(event) {
  if (event.key === 'Enter') addTask();
});

// ページを開いたときにタスク一覧を読み込む
loadTasks();
</script>
</body>
</html>
`))

// indexPageHandler はブラウザからのリクエストにHTMLページを返します。
func indexPageHandler(responseWriter http.ResponseWriter, request *http.Request) {
	responseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexPage.Execute(responseWriter, nil); err != nil {
		log.Printf("テンプレート実行エラー: %v", err)
	}
}

func main() {
	store := NewTaskStore()

	// ルーター（URLとハンドラーの対応表）を作成する
	router := http.NewServeMux()

	router.HandleFunc("/", indexPageHandler)                      // トップページ（HTML）
	router.HandleFunc("/api/tasks", tasksHandler(store))          // タスク一覧の取得・追加
	router.HandleFunc("/api/tasks/{id}", taskHandler(store))      // 個別タスクの更新・削除

	log.Println("サーバー起動: http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}
