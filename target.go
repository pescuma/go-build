package build

import "github.com/pkg/errors"

type Targets struct {
	items map[string]*Target
}

type Target struct {
	Name         string
	Dependencies []string
	run          TargetRunFunc
}

type TargetRunFunc func() error

func (l *Targets) Get(name string) *Target {
	t, ok := l.items[name]
	if !ok {
		return nil
	}

	return t
}

func (l *Targets) Add(name string, dependencies []string, code TargetRunFunc) {
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

func (l *Targets) ComputeTargetRunOrder(name string) ([]string, error) {
	var result []string
	visited := map[string]int{}

	return l.dfs(result, visited, name)
}

func (l *Targets) dfs(result []string, visited map[string]int, name string) ([]string, error) {
	var err error

	v, ok := visited[name]
	if !ok {
		v = 0
	}

	switch v {
	case 1:
		return nil, errors.Errorf("cycle identified in targets graph")
	case 2:
		return result, nil
	}

	t := l.Get(name)
	if t == nil {
		return nil, errors.Errorf("unknown target: %v", name)
	}

	visited[name] = 1

	for _, dep := range t.Dependencies {
		result, err = l.dfs(result, visited, dep)
		if err != nil {
			return nil, err
		}
	}

	if t.run != nil {
		result = append(result, name)
	}

	visited[name] = 2

	return result, nil
}
