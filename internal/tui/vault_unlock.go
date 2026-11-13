package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
)

var ErrVaultUnlockCanceled = errors.New("vault unlock canceled")

type vaultUnlockFunc func([]byte) error

type vaultUnlockResultMsg struct{ err error }

type vaultUnlockModel struct {
	creating   bool
	confirming bool
	busy       bool
	done       bool
	cancelled  bool
	password   []rune
	input      []rune
	message    string
	unlock     vaultUnlockFunc
	width      int
}

func newVaultUnlockModel(creating bool, unlock vaultUnlockFunc) *vaultUnlockModel {
	return &vaultUnlockModel{creating: creating, unlock: unlock, width: 72}
}

func (m *vaultUnlockModel) Init() tea.Cmd { return nil }

func (m *vaultUnlockModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := message.(vaultUnlockResultMsg); ok {
		m.busy = false
		if result.err != nil {
			m.clearSecrets()
			m.confirming = false
			m.message = "Could not unlock the vault. Check the master password and try again."
			return m, nil
		}
		m.clearSecrets()
		m.done = true
		return m, tea.Quit
	}
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(32, message.Width)
	case tea.KeyPressMsg:
		if message.Code == tea.KeyEscape || (message.Mod == tea.ModCtrl && message.Code == 'c') {
			m.clearSecrets()
			m.cancelled = true
			return m, tea.Quit
		}
		if m.busy || m.done || m.cancelled {
			return m, nil
		}
		switch {
		case message.Code == tea.KeyEnter || message.Code == tea.KeyReturn:
			return m.submit()
		case message.Code == tea.KeyBackspace || message.Code == tea.KeyDelete:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case message.Mod == tea.ModCtrl && message.Code == 'u':
			clearRunes(m.input)
			m.input = nil
		default:
			for _, r := range message.Text {
				if r >= ' ' && r != 0x7f {
					m.input = append(m.input, r)
				}
			}
		}
	}
	return m, nil
}

func (m *vaultUnlockModel) submit() (tea.Model, tea.Cmd) {
	if len(m.input) == 0 {
		m.message = "Enter a master password to continue."
		return m, nil
	}
	m.message = ""
	if m.creating && !m.confirming {
		m.password = append(m.password[:0], m.input...)
		clearRunes(m.input)
		m.input = nil
		m.confirming = true
		return m, nil
	}
	if m.confirming && !equalRunes(m.password, m.input) {
		m.clearSecrets()
		m.confirming = false
		m.message = "The passwords did not match. Enter a new master password."
		return m, nil
	}
	passwordRunes := m.input
	if m.confirming {
		passwordRunes = m.password
	}
	password := []byte(string(passwordRunes))
	clearRunes(m.input)
	m.input = nil
	clearRunes(m.password)
	m.password = nil
	m.busy = true
	unlock := m.unlock
	return m, func() tea.Msg {
		defer clear(password)
		return vaultUnlockResultMsg{err: unlock(password)}
	}
}

func (m *vaultUnlockModel) View() tea.View {
	title := "Unlock AACT shared vault"
	label := "Master password"
	if m.creating {
		title = "Create AACT shared vault"
		if m.confirming {
			label = "Confirm master password"
		}
	}
	mask := strings.Repeat("•", len(m.input))
	if m.busy {
		mask = "Unlocking…"
	}
	content := fmt.Sprintf("%s\n\n%s\n  %s\n", title, label, mask)
	if m.message != "" {
		content += "\n" + m.message + "\n"
	}
	content += "\nEnter to continue · Esc to cancel"
	if m.creating {
		content += "\nA new vault is saved in AACT state."
	}
	return tea.View{Content: content}
}

func (m *vaultUnlockModel) clearSecrets() {
	clearRunes(m.password)
	clearRunes(m.input)
	m.password = nil
	m.input = nil
}

func clearRunes(value []rune) {
	for i := range value {
		value[i] = 0
	}
}

func equalRunes(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	var difference rune
	for i := range left {
		difference |= left[i] ^ right[i]
	}
	return difference == 0
}

// RunVaultUnlock shows the unlock/create screen before launching the main TUI.
func RunVaultUnlock(ctx context.Context, input io.Reader, output io.Writer, creating bool, unlock vaultUnlockFunc) error {
	model := newVaultUnlockModel(creating, unlock)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output))
	final, err := program.Run()
	if err != nil {
		return err
	}
	result, ok := final.(*vaultUnlockModel)
	if !ok {
		return errors.New("vault unlock screen exited unexpectedly")
	}
	if result.cancelled {
		return ErrVaultUnlockCanceled
	}
	if !result.done {
		return errors.New("vault unlock screen exited without unlocking")
	}
	return nil
}
