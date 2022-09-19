package build

import "github.com/pkg/errors"

type Targets struct {
	items map[string]*Target
}

func (l *Targets) Get(name string) *Target {
	t, ok := l.items[name]
	if !ok {
		return nil
	}

	return t
}

func (l *Targets) Add(name string, dependencies []string, code TargetRunFunc) *Target {
	_, ok := l.items[name]
	if ok {
		panic("Target already exists: " + name)
	}

	t := &Target{
		Name:         name,
		Dependencies: dependencies,
		run:          code,
	}

	l.items[name] = t

	return t
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

type Target struct {
	Name         string
	Dependencies []string
	run          TargetRunFunc
}

func (t *Target) AddDependency(dep *Target) {
	t.Dependencies = append(t.Dependencies, dep.Name)
}

type TargetRunFunc func() error
