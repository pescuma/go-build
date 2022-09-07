package build

type TargetRunFunc func() error

type Target struct {
	Name         string
	Dependencies []string
	run          TargetRunFunc
}

type TargetList struct {
	items map[string]*Target
}

func (l *TargetList) Get(name string) *Target {
	t, ok := l.items[name]
	if !ok {
		return nil
	}

	return t
}

func (l *TargetList) Add(name string, dependencies []string, code TargetRunFunc) {
	_, ok := l.items[name]
	if ok {
		panic("Target already exists: " + name)
	}

	l.items[name] = &Target{
		Name:         name,
		Dependencies: dependencies,
		run:          code,
	}
}
