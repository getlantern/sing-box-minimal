package rule

import (
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/processpath"
	C "github.com/sagernet/sing-box/constant"
)

var _ RuleItem = (*ProcessPathItem)(nil)

type ProcessPathItem struct {
	processes  []string
	processMap map[string]bool
}

func NewProcessPathItem(processNameList []string) *ProcessPathItem {
	rule := &ProcessPathItem{
		processes:  processNameList,
		processMap: make(map[string]bool),
	}
	for _, processName := range processNameList {
		rule.processMap[processpath.Normalize(processName)] = true
	}
	return rule
}

func (r *ProcessPathItem) Match(metadata *adapter.InboundContext) bool {
	if metadata.ProcessInfo == nil {
		return false
	}
	path := metadata.ProcessInfo.ProcessPath
	if C.IsWindows {
		path = strings.ToLower(path)
	}
	if path != "" && r.processMap[path] {
		return true
	}
	if C.IsWindows && path != "" && r.processMap[processpath.Normalize(path)] {
		return true
	}
	if C.IsAndroid {
		for _, packageName := range metadata.ProcessInfo.AndroidPackageNames {
			if r.processMap[packageName] {
				return true
			}
		}
	}
	return false
}

func (r *ProcessPathItem) String() string {
	var description string
	pLen := len(r.processes)
	if pLen == 1 {
		description = "process_path=" + r.processes[0]
	} else {
		description = "process_path=[" + strings.Join(r.processes, " ") + "]"
	}
	return description
}
