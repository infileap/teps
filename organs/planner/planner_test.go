package planner

import (
	"strconv"
	"strings"
	"testing"
)

func sk(ref, name string, in, out []string, rel float64) Skill {
	return Skill{Ref: ref, Name: name, In: in, Out: out, Rel: rel}
}

// 一个最小的世界：read 把 path 变成 text，upper 把 text 变成 text。
func world() []Skill {
	return []Skill{
		sk("h-read", "read.file", []string{"path"}, []string{"text"}, 0.99),
		sk("h-upper", "text.upper", []string{"text"}, []string{"text"}, 0.95),
		sk("h-count", "text.count", []string{"text"}, []string{"count"}, 0.90),
	}
}

func TestAFoundChainIsReturnedInOrder(t *testing.T) {
	res := Search(world(), Goal{From: []string{"path"}, To: []string{"count"}, MaxDepth: 4, Floor: 0.1})
	if len(res.Proposals) == 0 {
		t.Fatal("read.file then text.count reaches count")
	}
	got := res.Proposals[0]
	if len(got.Children) != 2 || got.Children[0] != "h-read" || got.Children[1] != "h-count" {
		t.Fatalf("want read then count, got %v", got.Names)
	}
	if got.Depth != 2 {
		t.Fatalf("want depth 2, got %d", got.Depth)
	}
}

// 类型是有方向的：count 不再产出 text，所以反着接不通。
func TestTypeDirectionIsRespected(t *testing.T) {
	res := Search(world(), Goal{From: []string{"count"}, To: []string{"text"}, MaxDepth: 4, Floor: 0.1})
	if len(res.Proposals) != 0 {
		t.Fatalf("nothing turns a count back into text, got %v", res.Proposals[0].Names)
	}
}

func TestAnUnreachableGoalYieldsNothingAndSaysSo(t *testing.T) {
	res := Search(world(), Goal{From: []string{"path"}, To: []string{"image"}, MaxDepth: 6, Floor: 0.1})
	if len(res.Proposals) != 0 {
		t.Fatal("nothing produces an image here")
	}
	if res.States < 2 {
		t.Fatal("the search must still have explored, otherwise it proved nothing")
	}
}

// ★ 可靠性剪枝：十步 0.99 是 0.90。乘积本身就是「偏好短链」的机制，
// 不需要谁去写一条「别太长」的规则。
// chain 造一条 t0→t1→…→tn 的直线，每步同样可靠。
func chain(n int, rel float64) []Skill {
	out := make([]Skill, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, sk(
			"h-"+strconv.Itoa(i), "s"+strconv.Itoa(i),
			[]string{"t" + strconv.Itoa(i)}, []string{"t" + strconv.Itoa(i+1)}, rel))
	}
	return out
}

// ★ 可靠性剪枝：十步 0.99 是 0.90。乘积本身就是「偏好短链」的机制，
// 不需要谁去写一条「别太长」的规则。
func TestReliabilityPruningDropsLongChainsWithoutAnyoneSayingSo(t *testing.T) {
	skills := chain(10, 0.99)
	// 两步：0.99^2 = 0.9801，在 0.95 之上。
	near := Search(skills, Goal{From: []string{"t0"}, To: []string{"t2"}, MaxDepth: 10, Floor: 0.95})
	if len(near.Proposals) == 0 {
		t.Fatal("0.99^2 is above the floor and must be proposed")
	}
	// 九步：0.99^9 = 0.9135，在 0.95 之下。
	far := Search(skills, Goal{From: []string{"t0"}, To: []string{"t9"}, MaxDepth: 10, Floor: 0.95})
	if len(far.Proposals) != 0 {
		t.Fatalf("0.99^9 falls below the floor and must not be proposed, got %v", far.Proposals[0].Names)
	}
	found := false
	for _, d := range far.Dropped {
		if strings.Contains(d.Reason, "below the floor") {
			found = true
		}
	}
	if !found {
		t.Fatal("and the refusal must be recorded, not silent")
	}
}

// ★ 支配性：到达同一组类型的更可靠前缀胜出。这不是启发式——
// 前缀之后的续法只取决于可用的类型集合，与怎么到达无关。
func TestTheMoreReliableRouteToTheSameTypesDominates(t *testing.T) {
	skills := []Skill{
		sk("h-good", "good", []string{"a"}, []string{"c"}, 0.99),
		sk("h-bad", "bad", []string{"a"}, []string{"c"}, 0.50),
		sk("h-step", "step", []string{"a"}, []string{"c"}, 0.80),
	}
	res := Search(skills, Goal{From: []string{"a"}, To: []string{"c"}, MaxDepth: 3, Floor: 0.1})
	if len(res.Proposals) == 0 {
		t.Fatal("three routes reach c")
	}
	if res.Proposals[0].Children[0] != "h-good" {
		t.Fatalf("the most reliable route must come first, got %v", res.Proposals[0].Names)
	}
	found := false
	for _, d := range res.Dropped {
		if strings.Contains(d.Reason, "less reliable route") {
			found = true
		}
	}
	if !found {
		t.Fatal("the dominated route must be reported, not silently dropped")
	}
}

// 一个不声明产出的技能，对规划器来说不是「总是可用」，而是契约缺失。
// 在没声明的类型上做组合就是猜。
func TestASkillWithNoOutputTypesIsUnusable(t *testing.T) {
	skills := []Skill{
		sk("h-sink", "sink", []string{"text"}, nil, 0.99),
		sk("h-read", "read.file", []string{"path"}, []string{"text"}, 0.99),
	}
	res := Search(skills, Goal{From: []string{"path"}, To: []string{"text"}, MaxDepth: 3, Floor: 0.1})
	if len(res.Unusable) != 1 || res.Unusable[0].Ref != "h-sink" {
		t.Fatalf("want exactly the sink reported as unusable, got %v", res.Unusable)
	}
	if !strings.Contains(res.Unusable[0].Reason, "no output types") {
		t.Fatalf("the reason must say why, got %q", res.Unusable[0].Reason)
	}
}

func TestASkillWithNoEvidenceIsUnusable(t *testing.T) {
	skills := []Skill{sk("h-zero", "zero", []string{"a"}, []string{"b"}, 0)}
	res := Search(skills, Goal{From: []string{"a"}, To: []string{"b"}, MaxDepth: 2, Floor: 0.1})
	if len(res.Unusable) != 1 {
		t.Fatalf("want the zero-reliability skill reported, got %v", res.Unusable)
	}
	if len(res.Proposals) != 0 {
		t.Fatal("a step with no evidence must not be planned around")
	}
}

// 同一个技能可以用两次：state 是类型集合，不是「用过的技能集合」。
func TestASkillMayBeUsedTwice(t *testing.T) {
	skills := []Skill{
		sk("h-a", "a", []string{"x"}, []string{"y"}, 0.99),
		sk("h-b", "b", []string{"y"}, []string{"x"}, 0.99),
		sk("h-c", "c", []string{"x", "y"}, []string{"z"}, 0.99),
	}
	res := Search(skills, Goal{From: []string{"x"}, To: []string{"z"}, MaxDepth: 3, Floor: 0.1})
	if len(res.Proposals) == 0 {
		t.Fatal("x -> y -> x -> z is a legitimate chain")
	}
	longest := res.Proposals[0]
	for _, p := range res.Proposals {
		if len(p.Children) > len(longest.Children) {
			longest = p
		}
	}
	if len(longest.Children) < 2 {
		t.Fatalf("want at least one chain that reuses a skill, got %v", longest.Names)
	}
}

func TestMaxDepthBoundsTheSearch(t *testing.T) {
	// 需要三步才到 z，但只给两步。
	skills := []Skill{
		sk("h-a", "a", []string{"x"}, []string{"y"}, 0.99),
		sk("h-b", "b", []string{"y"}, []string{"w"}, 0.99),
		sk("h-c", "c", []string{"w"}, []string{"z"}, 0.99),
	}
	if res := Search(skills, Goal{From: []string{"x"}, To: []string{"z"}, MaxDepth: 2, Floor: 0.1}); len(res.Proposals) != 0 {
		t.Fatal("two steps cannot reach z")
	}
	if res := Search(skills, Goal{From: []string{"x"}, To: []string{"z"}, MaxDepth: 3, Floor: 0.1}); len(res.Proposals) == 0 {
		t.Fatal("three steps can")
	}
}

// ★ 同一个输入必须给同一个顺序。map 迭代是随机的，
// 一个每次答案都不同的搜索没法被推理。
func TestTheOrderIsDeterministic(t *testing.T) {
	var first []string
	for i := 0; i < 8; i++ {
		res := Search(world(), Goal{From: []string{"path"}, To: []string{"count"}, MaxDepth: 4, Floor: 0.1})
		var names []string
		for _, p := range res.Proposals {
			names = append(names, strings.Join(p.Names, ">"))
		}
		joined := strings.Join(names, " | ")
		if i == 0 {
			first = []string{joined}
			continue
		}
		if joined != first[0] {
			t.Fatalf("run %d differs:\n  %s\n  %s", i, first[0], joined)
		}
	}
}

func TestTheCatalogOrderDoesNotChangeTheAnswer(t *testing.T) {
	a := Search(world(), Goal{From: []string{"path"}, To: []string{"count"}, MaxDepth: 4, Floor: 0.1})
	w := world()
	w[0], w[2] = w[2], w[0]
	b := Search(w, Goal{From: []string{"path"}, To: []string{"count"}, MaxDepth: 4, Floor: 0.1})
	if len(a.Proposals) != len(b.Proposals) {
		t.Fatalf("same world, different catalog order, different proposal count: %d vs %d", len(a.Proposals), len(b.Proposals))
	}
	for i := range a.Proposals {
		if strings.Join(a.Proposals[i].Children, "|") != strings.Join(b.Proposals[i].Children, "|") {
			t.Fatalf("proposal %d differs between catalog orders", i)
		}
	}
}

// 更可靠的排前面；一样可靠时短的排前面。
func TestRankingIsByReliabilityThenLength(t *testing.T) {
	skills := []Skill{
		sk("h-fast", "fast", []string{"a"}, []string{"z"}, 0.90),
		sk("h-good1", "good1", []string{"a"}, []string{"m"}, 0.99),
		sk("h-good2", "good2", []string{"m"}, []string{"z"}, 0.99),
	}
	res := Search(skills, Goal{From: []string{"a"}, To: []string{"z"}, MaxDepth: 3, Floor: 0.1})
	if len(res.Proposals) < 2 {
		t.Fatalf("two routes reach z, got %d", len(res.Proposals))
	}
	// 0.99*0.99 = 0.9801 > 0.90，所以两步那条排前面——可靠优先于短。
	if len(res.Proposals[0].Children) != 2 {
		t.Fatalf("the more reliable route wins even though it is longer, got %v", res.Proposals[0].Names)
	}
	if res.Proposals[0].Rel <= res.Proposals[1].Rel {
		t.Fatal("proposals must be ordered by reliability")
	}
}

func TestMaxProposalsCapsTheAnswerWithoutHidingTheDrops(t *testing.T) {
	skills := []Skill{
		sk("h-1", "one", []string{"a"}, []string{"z"}, 0.99),
		sk("h-2", "two", []string{"a"}, []string{"z"}, 0.98),
		sk("h-3", "three", []string{"a"}, []string{"z"}, 0.97),
	}
	res := Search(skills, Goal{From: []string{"a"}, To: []string{"z"}, MaxDepth: 2, Floor: 0.1, MaxProposals: 1})
	if len(res.Proposals) != 1 {
		t.Fatalf("want one proposal, got %d", len(res.Proposals))
	}
	if res.Proposals[0].Children[0] != "h-1" {
		t.Fatal("the cap must keep the best one")
	}
}

// 目标一开始就满足 ⇒ 空组合。空组合会被 store 拒（a composition of nothing
// would succeed by having done nothing），所以搜索**不提议它**，而且这不是遗漏。
func TestAnAlreadyMetGoalYieldsNoProposal(t *testing.T) {
	res := Search(world(), Goal{From: []string{"path", "count"}, To: []string{"count"}, MaxDepth: 3, Floor: 0.1})
	for _, p := range res.Proposals {
		if len(p.Children) == 0 {
			t.Fatal("the empty chain is not a composition, and must not be proposed as one")
		}
	}
}

func TestEveryProposalExplainsItself(t *testing.T) {
	res := Search(world(), Goal{From: []string{"path"}, To: []string{"count"}, MaxDepth: 4, Floor: 0.1})
	if len(res.Proposals) == 0 {
		t.Fatal("nothing found")
	}
	for _, p := range res.Proposals {
		if !strings.Contains(p.Why, "inputs are among the types produced") {
			t.Fatalf("a proposal must say what makes it a candidate, got %q", p.Why)
		}
		if len(p.Reaches) == 0 {
			t.Fatal("a proposal must report what it reaches")
		}
		if p.Rel <= 0 {
			t.Fatal("a proposal must report its reliability")
		}
	}
}
