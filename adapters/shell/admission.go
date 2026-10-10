package main

import (
	"strings"
	"time"

	"sansheng/core"
	"sansheng/organs/pool"
)

// 准入：算力池第一次真的决定一次执行能不能跑。
//
// 池能定价，但在此之前没有任何东西为一次执行去挑资源——执行器总是本机 spawn，
// 而「在哪台机器上跑」从来不是一个决定。这个文件把那个决定接上：
// 调用方点名一个资源（或者让池去挑），执行器就为这次尝试**占一个槽**，
// 跑完释放。槽占不到，执行就不发生——容量因此是一条真的约束，不是一份登记表。
//
// 三条不显然的边界：
//
//  1. **不点名就照旧。** 没有资源被声明时，「选哪台机器」没有可选项，
//     这次执行跑在它本来就在的机器上。把它记成某个隐式的资源会凭空造出一个
//     没人登记过的东西，所以记的是空字符串加一句说明。
//
//  2. **执行器只认得本机的资源。** 远端 GPU 登记得上，但本组件没有把进程送到
//     另一台机器上去的手段。把 `gpu.far` 记成一次执行发生的地方，就是在链上
//     写一句假话——而链是唯一被信任的东西。所以非本机的资源被**明确拒绝**，
//     理由说清是「这个组件做不到」，不是「这个资源不好」。
//
//  3. **租约必须比进程活得久。** 进程还没被杀掉、槽就先过期的话，容量会在一次
//     正在运行的执行底下被别人拿走。

const (
	keyLease   = ":lease"
	keyRelease = ":release"
	// leaseSlack 是租约比硬时限多活的毫秒数。
	leaseSlack = 5000
	// localOnly 说明这一层能做什么。它是一句能力声明，不是策略。
	localOnly = "this executor runs processes on the machine it is already on: it can admit work to a local resource and cannot dispatch to another kind"
)

// admission 报告一次执行被准许在哪里跑，以及它花了多少。
type admission struct {
	Resource string `json:"resource"`
	Kind     string `json:"kind,omitempty"`
	Holder   string `json:"holder,omitempty"`
	Money    int64  `json:"cost_money"`
	Expires  int64  `json:"expires,omitempty"`
	Note     string `json:"note"`
}

// admit 决定这次尝试跑在哪个资源上，并占住它的槽。
//
// want 是点名要的资源 id；pick 为真时由池从**本机**资源里挑最便宜的。
// 两者都没给，就是「不点名」，本次执行不受准入约束。
func (s *server) admit(attempt, want string, pick bool, work core.Budget, ttl int64) (*admission, *core.Refusal) {
	if want == "" && !pick {
		return &admission{
			Resource: "",
			Note:     "no resource was named, so no capacity was reserved: this ran where it always runs",
		}, nil
	}
	if s.pools == nil {
		return nil, core.Refuse(core.CodeBadRequest, "no compute pool is attached")
	}

	now := time.Now().UnixMilli()
	s.pools.lock()
	defer s.pools.unlock()

	var r pool.Resource
	if want != "" {
		found, ok := s.pools.find(want)
		if !ok {
			return nil, core.Refuse(core.CodeBadRequest, "no such resource: "+want)
		}
		if found.Kind != pool.KindLocal {
			// 登记它没问题，在它上面跑不是这个组件做的事。说清是哪一个做不到。
			return nil, core.Refuse(core.CodeBadRequest,
				"resource "+found.ID+" is "+string(found.Kind)+": "+localOnly)
		}
		r = found
	} else {
		locals := make([]pool.Resource, 0, len(s.pools.resources))
		for _, c := range s.pools.resources {
			if c.Kind == pool.KindLocal {
				locals = append(locals, c)
			}
		}
		chosen, refusal := pool.Cheapest(locals, work, core.Budget{
			// 准入只关心钱与 token：毫秒那一维是执行器的硬时限管的，
			// 而池里的资源按毫秒收的是**钱**，不是时间。
			Money:  1 << 40,
			Tokens: 1 << 40,
		}, s.pools.leases, now)
		if refusal != nil {
			return nil, refusal
		}
		r = chosen
	}

	lease, refusal := pool.Acquire(r, s.pools.leases, attempt, now, ttl)
	if refusal != nil {
		return nil, refusal
	}
	if _, refusal := s.pools.append(poolRecord{
		Rec: "pool", Op: "lease", ID: r.ID, Holder: lease.Holder, Expires: lease.Expires,
	}, attempt+keyLease); refusal != nil {
		return nil, refusal
	}
	c := pool.Cost(r, work)
	return &admission{
		Resource: r.ID,
		Kind:     string(r.Kind),
		Holder:   lease.Holder,
		Money:    c.Money,
		Expires:  lease.Expires,
		Note:     "capacity was reserved for this attempt and is released when it ends",
	}, nil
}

// discharge 释放这次尝试占的槽。
//
// 它必须能被安全地重复调用：链上重复的释放记录在重放时是幂等的（Release 是
// 「把这个持有者的租约都去掉」），而一次执行可能因为各种原因走到这里两次。
func (s *server) discharge(attempt string) {
	if s.pools == nil {
		return
	}
	s.pools.lock()
	defer s.pools.unlock()
	s.pools.append(poolRecord{Rec: "pool", Op: "release", Holder: attempt}, attempt+keyRelease)
}

// poolSummary 给响应里那句「跑在哪」用。
func poolSummary(a *admission) map[string]any {
	if a == nil {
		return nil
	}
	m := map[string]any{"resource": a.Resource, "note": a.Note, "cost_money": a.Money}
	if a.Kind != "" {
		m["kind"] = a.Kind
	}
	if a.Holder != "" {
		m["holder"] = a.Holder
		m["expires"] = a.Expires
	}
	return m
}

// wantsPool 只是把「调用方有没有对资源说话」写成一个可读的名字。
func wantsPool(resource string, pick bool) bool {
	return strings.TrimSpace(resource) != "" || pick
}
