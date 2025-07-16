package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// 廣播遊戲狀態的 channel
var broadcast = make(chan BroadcastMsg)

// 玩家資訊
type PlayerInfo struct {
	Name  string
	Ready bool
	Conn  *websocket.Conn
}

// 房間資訊
type RoomInfo struct {
	Players map[*websocket.Conn]*PlayerInfo
	InGame  bool
	Mode    string // "single" or "multi"
}

var roomStates = make(map[string]*RoomInfo)

type BroadcastMsg struct {
	room string
	data []byte
}

// 取得房間內所有玩家名稱
func getRoomPlayerNames(room string) []string {
	names := []string{}
	for _, p := range roomStates[room].Players {
		names = append(names, p.Name)
	}
	return names
}

// 取得房間玩家狀態（名字 + Ready）
func getRoomPlayerStates(room string) []map[string]interface{} {
	states := []map[string]interface{}{}
	for _, p := range roomStates[room].Players {
		states = append(states, map[string]interface{}{
			"name":  p.Name,
			"ready": p.Ready,
		})
	}
	return states
}

// 廣播房間目前狀態（所有玩家 + Ready 狀態）
func broadcastRoomStatus(room string) {
	roomStatusMsg := map[string]interface{}{
		"type":    "roomStatus",
		"players": getRoomPlayerStates(room),
	}
	roomStatusJson, _ := json.Marshal(roomStatusMsg)
	for conn := range roomStates[room].Players {
		conn.WriteMessage(websocket.TextMessage, roomStatusJson)
	}
}

func main() {
	go handleMessages()

	http.HandleFunc("/ws", handleConnections)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("🚀 多人貪食蛇伺服器啟動於 http://localhost:7000")
	http.ListenAndServe(":7000", nil)
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	name := r.URL.Query().Get("name")
	if room == "" {
		room = "lobby"
	}
	if name == "" {
		name = "玩家"
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("❌ Upgrade Error:", err)
		return
	}
	defer ws.Close()

	// 初始化房間
	if roomStates[room] == nil {
		roomStates[room] = &RoomInfo{
			Players: make(map[*websocket.Conn]*PlayerInfo),
			InGame:  false,
		}
	}
	roomStates[room].Players[ws] = &PlayerInfo{Name: name, Ready: false, Conn: ws}

	fmt.Printf("✅ %s 加入房間 [%s]，目前人數：%d\n", name, room, len(roomStates[room].Players))

	// 廣播進房
	joinMsg := map[string]interface{}{
		"type":    "playerJoin",
		"name":    name,
		"count":   len(roomStates[room].Players),
		"players": getRoomPlayerNames(room),
	}
	joinJson, _ := json.Marshal(joinMsg)
	for conn := range roomStates[room].Players {
		conn.WriteMessage(websocket.TextMessage, joinJson)
	}

	// 進房後同步一次房間狀態
	broadcastRoomStatus(room)

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			fmt.Printf("❌ %s 離開房間 [%s]\n", name, room)
			delete(roomStates[room].Players, ws)

			// 廣播離房
			leaveMsg := map[string]interface{}{
				"type":    "playerLeave",
				"name":    name,
				"count":   len(roomStates[room].Players),
				"players": getRoomPlayerNames(room),
			}
			leaveJson, _ := json.Marshal(leaveMsg)
			for conn := range roomStates[room].Players {
				conn.WriteMessage(websocket.TextMessage, leaveJson)
			}

			// 離房後同步一次房間狀態
			broadcastRoomStatus(room)
			break
		}

		var data map[string]interface{}
		json.Unmarshal(msg, &data)
		msgType := data["type"].(string)

		if msgType == "ready" {
			mode := data["mode"].(string)
			roomStates[room].Mode = mode
			roomStates[room].Players[ws].Ready = true

			// Ready 後同步一次房間狀態
			broadcastRoomStatus(room)

			// 計算房間狀態
			playerCount := len(roomStates[room].Players)
			readyCount := 0
			allReady := true
			for _, p := range roomStates[room].Players {
				if p.Ready {
					readyCount++
				} else {
					allReady = false
				}
			}

			if mode == "single" {
				// 單人模式直接開始
				startMsg := `{"type":"startGame","countdown":3}`
				roomStates[room].InGame = true
				ws.WriteMessage(websocket.TextMessage, []byte(startMsg))
			} else if mode == "multi" {
				// 多人模式：至少2人且全員Ready才開始
				if playerCount >= 2 && allReady {
					startMsg := `{"type":"startGame","countdown":3}`
					roomStates[room].InGame = true
					for conn := range roomStates[room].Players {
						conn.WriteMessage(websocket.TextMessage, []byte(startMsg))
					}
				} else {
					// 正確顯示 Ready 人數
					fmt.Printf("⚠️ 房間[%s] Ready人數不足 Ready:%d/%d\n", room, readyCount, playerCount)
					waitMsg := fmt.Sprintf(`{"type":"waiting","msg":"等待其他玩家 Ready… (%d/%d)"}`, readyCount, playerCount)
					for conn := range roomStates[room].Players {
						conn.WriteMessage(websocket.TextMessage, []byte(waitMsg))
					}
				}
			}

		} else if msgType == "state" {
			// 遊戲同步封包
			broadcast <- BroadcastMsg{room: room, data: msg}

		} else if msgType == "gameOver" {
			// 有玩家達成勝利 → 廣播給全房間
			for conn := range roomStates[room].Players {
				conn.WriteMessage(websocket.TextMessage, msg)
				// 遊戲結束 → 重置 Ready
				roomStates[room].Players[conn].Ready = false
			}
			roomStates[room].InGame = false

			// 遊戲結束後更新一次房間狀態
			broadcastRoomStatus(room)
		}
	}
}

func handleMessages() {
	for {
		b := <-broadcast
		for client := range roomStates[b.room].Players {
			err := client.WriteMessage(websocket.TextMessage, b.data)
			if err != nil {
				fmt.Println("❌ 廣播錯誤，移除玩家")
				client.Close()
				delete(roomStates[b.room].Players, client)
			}
		}
	}
}
