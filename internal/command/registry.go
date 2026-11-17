package command

import "github.com/disgoorg/disgo/discord"

// Registry は登録されたコマンドを名前で引く。
type Registry struct {
	commands []Command
	byName   map[string]Command
}

// NewRegistry は commands を登録順に保持する Registry を返す。名前が重複したら panic する。
func NewRegistry(commands ...Command) *Registry {
	r := &Registry{commands: commands, byName: make(map[string]Command, len(commands))}
	for _, c := range commands {
		name := c.Definition().Name
		if _, exists := r.byName[name]; exists {
			panic("command: duplicate command name: " + name)
		}
		r.byName[name] = c
	}
	return r
}

// Find は名前からコマンドを探す。
func (r *Registry) Find(name string) (Command, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// Definitions は登録した順にコマンドの定義を返す。
func (r *Registry) Definitions() []discord.ApplicationCommandCreate {
	definitions := make([]discord.ApplicationCommandCreate, 0, len(r.commands))
	for _, c := range r.commands {
		definitions = append(definitions, c.Definition())
	}
	return definitions
}
