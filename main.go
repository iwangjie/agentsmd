package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"agentsmd/internal/desk"
)

func main() {
	// One copy of the app owns AGENTS.md. A second launch hands its command
	// line to the running one and exits, so two windows can never compile over
	// each other's work.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	stateDir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		stateDir = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "AgentsMD")
	}

	service, err := desk.New("", stateDir)
	if err != nil {
		log.Fatal(err)
	}
	mygo.BindAs("Agents", service)

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           "AgentsMD",
			Width:           1180,
			Height:          760,
			MinWidth:        900,
			MinHeight:       560,
			BackgroundColor: "light-dark(#f5f5f7, #111113)",
			StateKey:        "main",
			URL:             "/",
			TitleBarStyle:   mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{
				X: 18,
				Y: 22,
			},
		})
		desk.Attach(service, win)
		mygo.App.SetMenu(appMenu(service))
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// appMenu is the native menu bar: the file and edit menus the app needs, plus
// a window menu. Roles carry their own labels and shortcuts.
func appMenu(service *desk.Service) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "在访达中显示 AGENTS.md", Accelerator: "CmdOrCtrl+Shift+O", Click: func(*mygo.MenuItem, *mygo.Window) {
				_ = service.Reveal()
			}},
			{Label: "打开备份文件夹", Click: func(*mygo.MenuItem, *mygo.Window) {
				_ = service.RevealBackups()
			}},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	})
}
