package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up           key.Binding
	Down         key.Binding
	Enter        key.Binding
	Tab          key.Binding
	Kill         key.Binding
	AutoForward  key.Binding
	HideChildren key.Binding
	Space        key.Binding
	Refresh      key.Binding
	Escape       key.Binding
	Quit         key.Binding
	CtrlC        key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
	),
	Kill: key.NewBinding(
		key.WithKeys("ctrl+k"),
	),
	AutoForward: key.NewBinding(
		key.WithKeys("ctrl+a"),
	),
	HideChildren: key.NewBinding(
		key.WithKeys("ctrl+h"),
	),
	Space: key.NewBinding(
		key.WithKeys(" "),
	),
	Refresh: key.NewBinding(
		key.WithKeys("ctrl+r"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q"),
	),
	CtrlC: key.NewBinding(
		key.WithKeys("ctrl+c"),
	),
}
