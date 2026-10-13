package main

import (
	"teps/core"
)

// 帧族的声明表 —— 它同时是分派表，也是 `ops` 的回答。
//
// # 为什么是一张表读给两个调用方
//
// `adapters/mcp/main.go` 的包注释里写着这个项目欠自己的一件事：
//
//	The follow-up worth doing is for the kernel to grow an op that reports its
//	own op set, so a client can discover them without anyone writing them down
//	twice.
//
// 一个主机接进来只看得见一个工具（`kernel`），而它**没有办法知道 `t` 能填什么**。
// 它得去读源码或者读契约文档。那不是一个协议，那是一份要人去背的清单。
//
// 但「再写一份 op 清单给 `ops` 报」是错的：那就成了分派的第二份拷贝，而第二份
// 拷贝就是会烂掉的那一份。这个项目在「同一个事实写两遍、只有一处被更新」上
// 已经付过很多次学费，其中一次正是**帧类型是全局命名空间**（`compose.build`
// 与 `organs/composer` 抢同一个名字，两边都能跑，只有放在一起看才知道撞了）。
//
// 所以这里只有一张表。`handle` 按它分派，`ops` 按它作答 —— **分派和回答不可能
// 漂移，因为它们是同一张表**。
//
// # 表的顺序是承重的
//
// `skill.invoke` 与 `skill.compose` 必须排在 `skill.` 之前：技能店拥有
// `skill.publish` / `resolve` / `list` / `promote`，它会用「未知帧类型」回答
// `skill.invoke`。表按声明顺序匹配，第一条认领的胜出。

// claim 是一个处理器对自己拥有的帧类型的声明。
type claim struct {
	// Prefix 是它拥有的前缀族。与 Exact 二选一。
	Prefix string
	// Exact 是它拥有的那一个具体帧类型。`skill.invoke` 与 `skill.compose`
	// 同属一个处理器，但它们在命名空间里是两件事，所以声明两条。
	Exact string
	// Note 是一句话说明这个族是干什么的。它写在这里，因为这里是定义这个族
	// 的地方 —— 写在别处的第二份说明就是会烂掉的那一份。
	Note string
	// Handle 收到帧之后返回 true 表示「这是我的」。
	Handle func(core.Frame, *core.Frame) bool
}

// claims 是声明表，也是分派表。
//
// 它是方法而不是包级变量：处理器需要 `s`。每次分派都重建这个切片是刻意的 ——
// 一张在启动时算好、之后可以被改的表，和一张每次都是同一个值的表，是两件事，
// 而这里要的是后者。
func (s *server) claims() []claim {
	return []claim{
		{Prefix: "session.", Note: "会话状态机：谁在什么状态、能不能往下走", Handle: s.handleSession},
		// ★ compose.propose 也由这个处理器认领 —— 它住在 compose.go 的
		// handleSkillCompose 里。这一点此前**没有任何地方写下来**：它只是
		// 因为 handleExecute 恰好排在 handleKnowledge 之前而可达。
		// 改成表驱动之后它立刻掉了出来（规划器段九条断言全红），
		// 那正是这张表存在的理由。
		{Exact: "compose.propose", Note: "组合搜索：从类型里搜出没人写过的链，只提议不运行", Handle: s.handleExecute},
		{Exact: "skill.compose", Note: "把一个组合真的跑起来（组合就是技能，没有第二个发布口）", Handle: s.handleExecute},
		{Exact: "skill.invoke", Note: "把一个已注册的技能真的跑起来", Handle: s.handleExecute},
		{Prefix: "skill.", Note: "技能店：发布 / 解析 / 列表 / 升级通道", Handle: s.handleSkill},
		{Prefix: "memory.", Note: "记忆 ABI：写入不可变、更正只能靠取代", Handle: s.handleKnowledge},
		{Prefix: "compose.", Note: "组合的构建与提议（只算不跑）", Handle: s.handleKnowledge},
		{Prefix: "distill.", Note: "把一次成功的完整路径蒸成一条候选规则", Handle: s.handleDistill},
		{Prefix: "task.", Note: "任务入口 —— 率的分母", Handle: s.handleIntake},
		{Prefix: "escalation.", Note: "升级定价：先问价不花钱，付价必须带上依据的报价", Handle: s.handleEscalation},
		{Prefix: "reflex.", Note: "唯一一层零上下文的回答", Handle: s.handleReflex},
		{Prefix: "arbiter.", Note: "仲裁器：报价排序、后验、升级判定", Handle: s.handleDecision},
		{Prefix: "audit.", Note: "唯一一个不向任何器官提问的帧：它回的是那些读法读的东西本身", Handle: s.handleAudit},
		{Prefix: "metrics.", Note: "能力是乘积：报告每个因子有没有定义", Handle: s.handleMetrics},
		{Prefix: "pool.", Note: "算力登记表与租约：容量是租约不是计数", Handle: s.handlePool},
		{Prefix: "breaker.", Note: "脚本宿主熔断：连续起不来的宿主先歇一会", Handle: s.handleBreaker},
		{Prefix: "ops", Note: "这一张表本身", Handle: s.handleOps},
	}
}

// handleOps 回答「你能收哪些帧」。
//
// 它回的就是分派用的那张表。所以它不可能报出一个分派不了的 op，
// 也不可能漏掉一个分派得了的 op —— 那不是一条纪律，那是这张表的结构。
func (s *server) handleOps(f core.Frame, out *core.Frame) bool {
	if f.T != "ops" {
		return false
	}
	claims := s.claims()
	families := make([]map[string]any, 0, len(claims))
	for _, c := range claims {
		row := map[string]any{"note": c.Note}
		if c.Exact != "" {
			row["type"] = c.Exact
		} else {
			row["prefix"] = c.Prefix
		}
		families = append(families, row)
	}
	// 按表序报出，**不排序**：表的顺序是承重的（第一条认领的胜出），
	// 一个按字母排序的清单会让人以为顺序无所谓。
	out.Body = mustJSON(map[string]any{
		"families": families,
		"protocol": core.ProtocolVersion,
		// 这句话要说出来，否则读的人会以为这是一份完整的 op 清单：
		// 它报的是**族**，不是每个族里的每一个 op。
		"note": "these are the frame families the dispatcher claims, in claim order; the ops inside a family are the family's business",
	})
	return true
}
