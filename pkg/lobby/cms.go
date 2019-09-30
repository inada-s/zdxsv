package lobby

import (
	"github.com/icza/gowut/gwu"
	"zdxsv/pkg/lobby/message"
	"strconv"
	"fmt"
	"encoding/hex"
)

type MyButtonHandler struct {
	counter int
	text    string
}

func (h *MyButtonHandler) HandleEvent(e gwu.Event) {
	if b, isButton := e.Src().(gwu.Button); isButton {
		b.SetText(b.Text() + h.text)
		h.counter++
		b.SetToolTip("You've clicked " + strconv.Itoa(h.counter) + " times!")
		e.MarkDirty(b)
	}
}

func runCMS(app *App) {
	// Create and build a window
	win := gwu.NewWindow("main", "Test GUI Window")
	win.Style().SetFullWidth()
	win.SetHAlign(gwu.HACenter)
	win.SetCellPadding(2)

	lbUser := gwu.NewLabel("hoge")

	p := gwu.NewHorizontalPanel()
	p.Add(gwu.NewLabel("UserId:"))
	tb := gwu.NewTextBox("BND9PA")
	p.Add(tb)
	btn := gwu.NewButton("検索")
	btn.AddEHandlerFunc(func(e gwu.Event) {
		app.Locked(func(app *App) {
			id := tb.Text()
			fmt.Println(id)
			u, ok := app.users[id]
			if !ok {
				fmt.Println("user not found")
				return
			}
			lbUser.SetText(u.UserId + " " + u.Name + " " + u.Team)
			e.MarkDirty(lbUser)
		})
	}, gwu.ETypeClick)
	p.Add(btn)
	win.Add(p)

	win.Add(lbUser)

	np := gwu.NewPanel()
	np.Add(gwu.NewLabel("ServerNotice Tester"))
	np.Add(gwu.NewLabel("Command:"))
	commandText := gwu.NewTextBox("6202")
	np.Add(commandText)
	np.Add(gwu.NewLabel("Bytes:"))
	bytesText := gwu.NewTextBox("0000")
	np.Add(bytesText)

	sendBtn := gwu.NewButton("送信")
	sendBtn.AddEHandlerFunc(func(e gwu.Event) {
		app.Locked(func(app *App) {
			id := tb.Text()
			peer, ok := app.users[id]
			if !ok {
				fmt.Println("user not found")
				return
			}
			cmdId, err := strconv.ParseInt(commandText.Text(), 16, 32)
			if err != nil {
				fmt.Println(err)
				return
			}
			body, err := hex.DecodeString(bytesText.Text())
			if err != nil {
				fmt.Println(err)
				return
			}
			msg := message.NewServerNotice(uint16(cmdId))
			msg.Body = body
			peer.SendMessage(msg)
		})
	}, gwu.ETypeClick)
	np.Add(sendBtn)
	win.Add(np)

	// Button which changes window content
/*
	win.Add(gwu.NewLabel("I'm a label! Try clicking on the button=>"))
	btn := gwu.NewButton("Click me")
	btn.AddEHandler(&MyButtonHandler{text: ":-)"}, gwu.ETYPE_CLICK)
	win.Add(btn)
	btnsPanel := gwu.NewNaturalPanel()
	btn.AddEHandlerFunc(func(e gwu.Event) {
		// Create and add a new button...
		newbtn := gwu.NewButton("Extra #" + strconv.Itoa(btnsPanel.CompsCount()))
		newbtn.AddEHandlerFunc(func(e gwu.Event) {
			btnsPanel.Remove(newbtn) // ...which removes itself when clicked
			e.MarkDirty(btnsPanel)
		}, gwu.ETYPE_CLICK)
		btnsPanel.Insert(newbtn, 0)
		e.MarkDirty(btnsPanel)
	}, gwu.ETYPE_CLICK)
	win.Add(btnsPanel)

	// TextBox with echo
	p = gwu.NewHorizontalPanel()
	p.Add(gwu.NewLabel("Enter your name:"))
	tb := gwu.NewTextBox("")
	tb.AddSyncOnETypes(gwu.ETYPE_KEY_UP)
	p.Add(tb)
	p.Add(gwu.NewLabel("You entered:"))
	nameLabel := gwu.NewLabel("")
	nameLabel.Style().SetColor(gwu.CLR_RED)
	tb.AddEHandlerFunc(func(e gwu.Event) {
		nameLabel.SetText(tb.Text())
		e.MarkDirty(nameLabel)
	}, gwu.ETYPE_CHANGE, gwu.ETYPE_KEY_UP)
	p.Add(nameLabel)
	win.Add(p)
*/
	// Create and start a GUI server (omitting error check)
	server := gwu.NewServer("guitest", "localhost:8081")
	server.SetText("Test GUI App")
	server.AddWin(win)
	server.Start() // Also opens windows list in browser
}
