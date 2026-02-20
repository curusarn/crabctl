package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	Enter       key.Binding
	Kill        key.Binding
	AutoForward key.Binding
	Escape      key.Binding
	Quit        key.Binding
	CtrlC       key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
	),
	Kill: key.NewBinding(
		key.WithKeys("ctrl+k"),
	),
	AutoForward: key.NewBinding(
		key.WithKeys("ctrl+a"),
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
