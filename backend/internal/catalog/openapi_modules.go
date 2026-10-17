package catalog

import (
	"sort"
	"sync"
)

// Modules register their public contract during server assembly, before serving
// requests. Keeping this boundary here avoids a catalog -> module import cycle.
var openAPIModules = struct {
	sync.RWMutex
	build map[string]func(paths, schemas map[string]any)
}{build: make(map[string]func(paths, schemas map[string]any))}

func RegisterOpenAPIModule(name string, build func(paths, schemas map[string]any)) {
	if name == "" || build == nil {
		panic("invalid OpenAPI module")
	}
	openAPIModules.Lock()
	defer openAPIModules.Unlock()
	openAPIModules.build[name] = build
}

func addOpenAPIModules(paths, schemas map[string]any) {
	openAPIModules.RLock()
	names := make([]string, 0, len(openAPIModules.build))
	for name := range openAPIModules.build {
		names = append(names, name)
	}
	sort.Strings(names)
	builders := make([]func(map[string]any, map[string]any), 0, len(names))
	for _, name := range names {
		builders = append(builders, openAPIModules.build[name])
	}
	openAPIModules.RUnlock()
	for _, build := range builders {
		build(paths, schemas)
	}
}
