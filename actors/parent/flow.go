package parent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A Flow is the state machine a project's tasks move through (spike
// y8m0). Every state names a driver: an agent state spawns a session
// under its persona on entry and the agent itself asks to leave it; a
// human state waits for a principal with steer on the task; a
// children state waits for every child task to end. Flows are
// immutable versioned documents in the store — a task pins the
// version it started under, so editing a flow never moves a running
// task. Nothing of this lives in any repo: the product is the front
// door and the record.
type Flow struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// Initial is where a human-created task enters; Leaf where a
	// child task an agent carved out enters; Stuck is the human state
	// a task falls into when its agent keeps failing or was aborted.
	Initial string           `json:"initial"`
	Leaf    string           `json:"leaf"`
	Stuck   string           `json:"stuck"`
	States  map[string]State `json:"states"`
	// Personas are the system prompts agent states run under, by name.
	Personas map[string]string `json:"personas"`
}

// State is one node of a Flow.
type State struct {
	Driver string `json:"driver"`
	// Doc is one line for humans and for the agent choosing where to
	// go next.
	Doc string `json:"doc,omitempty"`
	// Persona names Flow.Personas (agent states only).
	Persona string `json:"persona,omitempty"`
	// Next lists the states this one may transition to. Empty ⇒
	// terminal.
	Next []string `json:"next,omitempty"`
}

// Drivers.
const (
	DriverAgent    = "agent"
	DriverHuman    = "human"
	DriverChildren = "children"
)

// Terminal reports whether the state has no exit.
func (s State) Terminal() bool { return len(s.Next) == 0 }

// Allows reports whether to is one of the state's exits.
func (s State) Allows(to string) bool {
	for _, n := range s.Next {
		if n == to {
			return true
		}
	}
	return false
}

var stateName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Validate checks the flow is well-formed: every reference resolves,
// agent states have a persona, the special states have the right
// driver, and no state is unreachable from Initial or Leaf.
func (f Flow) Validate() error {
	if !ValidID(f.ID) || f.Version < 1 {
		return errors.New("flow: needs an id and a version ≥ 1")
	}
	if len(f.States) == 0 {
		return errors.New("flow: no states")
	}
	for name, st := range f.States {
		if !stateName.MatchString(name) {
			return fmt.Errorf("flow: bad state name %q", name)
		}
		switch st.Driver {
		case DriverAgent:
			if _, ok := f.Personas[st.Persona]; !ok || st.Persona == "" {
				return fmt.Errorf("flow: state %s: agent state needs a persona", name)
			}
			if st.Terminal() {
				return fmt.Errorf("flow: state %s: an agent state must have an exit", name)
			}
		case DriverHuman:
		case DriverChildren:
			if st.Terminal() {
				return fmt.Errorf("flow: state %s: a children state must have an exit", name)
			}
		default:
			return fmt.Errorf("flow: state %s: driver must be agent|human|children", name)
		}
		for _, n := range st.Next {
			if _, ok := f.States[n]; !ok {
				return fmt.Errorf("flow: state %s: next %q is not a state", name, n)
			}
		}
	}
	for what, name := range map[string]string{"initial": f.Initial, "leaf": f.Leaf, "stuck": f.Stuck} {
		if _, ok := f.States[name]; !ok {
			return fmt.Errorf("flow: %s %q is not a state", what, name)
		}
	}
	if f.States[f.Stuck].Driver != DriverHuman {
		return errors.New("flow: stuck must be a human state")
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		for _, m := range f.States[n].Next {
			walk(m)
		}
	}
	walk(f.Initial)
	walk(f.Leaf)
	walk(f.Stuck)
	for name := range f.States {
		if !seen[name] {
			return fmt.Errorf("flow: state %s is unreachable", name)
		}
	}
	return nil
}

// StateNames is the states in a stable order: Initial first, then the
// rest alphabetically.
func (f Flow) StateNames() []string {
	names := make([]string, 0, len(f.States))
	for n := range f.States {
		if n != f.Initial {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return append([]string{f.Initial}, names...)
}

// ---- the transition an agent asks for ---------------------------------------

// Transition is the one operation a task knows (spike y8m0): leave the
// current state for To. An agent asks for it by ending its last message
// with a ```transition fenced block holding this JSON; a human asks via
// the API; the parent itself asks on behalf of a children state. Set
// updates the task's own fields on the way (what the framer and
// groomer are for); Children are carved out under the task when To is
// a children state; Followups are new root tasks in the project,
// entered at Initial with this task as their origin.
type Transition struct {
	To        string          `json:"to"`
	Note      string          `json:"note,omitempty"`
	Artifact  json.RawMessage `json:"artifact,omitempty"`
	Set       *TaskSet        `json:"set,omitempty"`
	Children  []TaskSpec      `json:"children,omitempty"`
	Followups []TaskSpec      `json:"followups,omitempty"`
}

// TaskSet is the fields a transition may rewrite on its task; nil
// pointers leave the field alone.
type TaskSet struct {
	Title *string `json:"title,omitempty"`
	Goal  *string `json:"goal,omitempty"`
	Why   *string `json:"why,omitempty"`
	Repo  *string `json:"repo,omitempty"`
	Ref   *string `json:"ref,omitempty"`
}

// TaskSpec is a task to create: the human form, and a child or
// follow-up an agent asks for.
type TaskSpec struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Goal        string `json:"goal,omitempty"`
	Why         string `json:"why,omitempty"`
	Repo        string `json:"repo,omitempty"`
	Ref         string `json:"ref,omitempty"`
}

// transitionFence is the block an agent ends with.
var transitionFence = regexp.MustCompile("(?s)```transition[ \\t]*\\r?\\n(.*?)\\r?\\n```")

// ParseTransition finds the last ```transition block in text. ok is
// false when there is none; err when the last one does not parse.
func ParseTransition(text string) (tr Transition, ok bool, err error) {
	ms := transitionFence.FindAllStringSubmatch(text, -1)
	if len(ms) == 0 {
		return tr, false, nil
	}
	body := strings.TrimSpace(ms[len(ms)-1][1])
	if err := json.Unmarshal([]byte(body), &tr); err != nil {
		return tr, true, fmt.Errorf("transition block: %w", err)
	}
	if tr.To == "" {
		return tr, true, errors.New("transition block: no \"to\"")
	}
	return tr, true, nil
}

// ---- the prompt an agent state runs under -----------------------------------

// promptInput is what RenderPrompt frames the persona with.
type promptInput struct {
	Project Project
	Goals   []Goal
	Task    Task
	Parent  *Task
	Last    *LogEntry // the transition that brought the task here
	Attempt int
	Branch  string // the task's result branch, when it has a repo
	PR      string // the PR an earlier session opened on it, if any
}

// RenderPrompt is the whole prompt for one agent session: the state's
// persona, the task's frame (project, goal, why — the values the task
// serves), what the previous step left, and the contract for leaving
// the state. The persona is a system prompt in spirit; #task carries
// one prompt, so it is the prompt's head.
func (f Flow) RenderPrompt(state string, in promptInput) string {
	st := f.States[state]
	var b strings.Builder
	b.WriteString(strings.TrimSpace(f.Personas[st.Persona]))
	b.WriteString("\n\n---\n# Task\n")
	fmt.Fprintf(&b, "Project: %s\n", in.Project.Name)
	if g := goalText(in.Goals, in.Task.Goal); g != "" {
		fmt.Fprintf(&b, "Goal: %s\n", g)
	} else {
		b.WriteString("Goal: (not framed yet)\n")
	}
	if in.Task.Why != "" {
		fmt.Fprintf(&b, "Because: %s\n", in.Task.Why)
	}
	fmt.Fprintf(&b, "Title: %s\n", in.Task.Title)
	if in.Task.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(in.Task.Description))
	}
	if in.Parent != nil {
		fmt.Fprintf(&b, "\nPart of: %s", in.Parent.Title)
		if in.Parent.Why != "" {
			fmt.Fprintf(&b, " — %s", in.Parent.Why)
		}
		b.WriteString("\n")
	}
	if in.Task.Repo != "" {
		fmt.Fprintf(&b, "Repo: %s", in.Task.Repo)
		if in.Task.Ref != "" {
			fmt.Fprintf(&b, "@%s", in.Task.Ref)
		}
		b.WriteString(" (cloned as your working directory)\n")
		if in.Branch != "" {
			fmt.Fprintf(&b, "Branch: %s (checked out; every session of this task commits here, the harness pushes it)\n", in.Branch)
		}
		if in.PR != "" {
			fmt.Fprintf(&b, "PR: %s (this task's, opened by an earlier session; the diff to review is there)\n", in.PR)
		}
	}
	fmt.Fprintf(&b, "State: %s (attempt %d)\n", state, in.Attempt)
	if in.Task.Error != "" {
		fmt.Fprintf(&b, "Previous attempt failed: %s\n", in.Task.Error)
	}
	if in.Last != nil {
		fmt.Fprintf(&b, "\n# Previous step\n%s → %s by %s", in.Last.From, in.Last.To, in.Last.By)
		if in.Last.Note != "" {
			fmt.Fprintf(&b, ": %s", in.Last.Note)
		}
		b.WriteString("\n")
		if len(in.Last.Artifact) > 0 && string(in.Last.Artifact) != "null" {
			fmt.Fprintf(&b, "Artifact:\n```json\n%s\n```\n", string(in.Last.Artifact))
		}
	}
	if len(in.Goals) > 0 {
		b.WriteString("\n# Project goals\n")
		for _, g := range in.Goals {
			fmt.Fprintf(&b, "- %s: %s\n", g.ID, g.Text)
		}
	}
	b.WriteString("\n# How to finish\n")
	b.WriteString("You leave this state by ending your final message with exactly one fenced block:\n\n")
	b.WriteString("```transition\n{\"to\": \"<state>\", \"note\": \"<one line for the next step>\", \"artifact\": {<what the next step needs>}}\n```\n\n")
	b.WriteString("Allowed states:\n")
	for _, n := range st.Next {
		fmt.Fprintf(&b, "- %s", n)
		if d := f.States[n].Doc; d != "" {
			fmt.Fprintf(&b, ": %s", d)
		}
		if f.States[n].Driver == DriverChildren {
			b.WriteString(" (requires \"children\": [{title, description, repo, ref}, …] — one per repo, each run on its own)")
		}
		b.WriteString("\n")
	}
	b.WriteString("Optional keys: \"set\": {title, goal, why, repo, ref} rewrites the task's own fields; ")
	b.WriteString("\"followups\": [{title, description}, …] files new tasks for what you noticed but must not do here.\n")
	b.WriteString("Without the block the run counts as failed and is retried; do not emit it until you are done.\n")
	return b.String()
}

func goalText(goals []Goal, id string) string {
	for _, g := range goals {
		if g.ID == id {
			return g.Text
		}
	}
	return ""
}

// ---- the default flow -------------------------------------------------------

// DefaultFlowID names the flow seeded into every store.
const DefaultFlowID = "default"

// DefaultFlow is the flow every project starts with: the grooming
// pipeline the swarm skills ran by hand (prep → worker → accept →
// merge → docs), with the human at capture, at the merge, and
// wherever an agent gets stuck. Versions are immutable (PutFlow) and a
// project pins the one it was created on; bump Version on any change.
//
// v2 (2026-10-19): the worker commits only — the child pushes the
// branch and opens the PR after the run (child/workspace.go), with a
// credential the agent never holds. v1 told it to "push, and open a
// PR"; the first real run spent six minutes credential-hunting and
// read the forge token out of /proc/1/environ (62a0ec24).
func DefaultFlow() Flow {
	return Flow{
		ID:      DefaultFlowID,
		Version: 2,
		Initial: "captured",
		Leaf:    "groomed",
		Stuck:   "needs-human",
		States: map[string]State{
			"captured": {Driver: DriverHuman, Doc: "raw; a person decides whether it is worth framing",
				Next: []string{"framed", "rejected"}},
			"framed": {Driver: DriverAgent, Persona: "framer", Doc: "tie the task to a goal and say why; dedupe; ask if unclear",
				Next: []string{"groomed", "needs-human", "rejected"}},
			"groomed": {Driver: DriverAgent, Persona: "groomer", Doc: "research once; write the brief and acceptance criteria; split by repo if needed",
				Next: []string{"executing", "delegated", "needs-human"}},
			"delegated": {Driver: DriverChildren, Doc: "waiting for the child tasks carved out of this one",
				Next: []string{"reviewing"}},
			"executing": {Driver: DriverAgent, Persona: "worker", Doc: "do the work in the repo; result is a branch/PR",
				Next: []string{"reviewing", "needs-human"}},
			"reviewing": {Driver: DriverAgent, Persona: "reviewer", Doc: "check the result against the brief; send back or hand to a person",
				Next: []string{"ready", "executing", "needs-human"}},
			"ready": {Driver: DriverHuman, Doc: "a person merges (or sends it back)",
				Next: []string{"landed", "executing", "rejected"}},
			"landed": {Driver: DriverAgent, Persona: "closer", Doc: "record what landed; file follow-ups",
				Next: []string{"done"}},
			"needs-human": {Driver: DriverHuman, Doc: "an agent asked for a person, or kept failing",
				Next: []string{"framed", "groomed", "executing", "reviewing", "rejected"}},
			"done":     {Driver: DriverHuman, Doc: "finished"},
			"rejected": {Driver: DriverHuman, Doc: "not doing this"},
		},
		Personas: map[string]string{
			"framer": `You are the framer. Your job is to turn a raw task into one that is worth doing and says why.
- Pick the project goal it serves (by id) and write one sentence "because …" that a worker will read first. Use "set": {"goal", "why", "title"}.
- If it duplicates or belongs inside something else, or is not worth it, go to rejected with the reason in "note".
- If you cannot tell what is wanted, go to needs-human with your question in "note" — at most three questions, concrete.
- Do not do the work. Do not plan it in detail; that is the next state. Read what you need to decide, nothing more.`,
			"groomer": `You are the groomer. The task is framed; make it executable.
- Research once so the worker never has to: what to change, where, what "done" looks like (acceptance criteria that can be checked), what to leave alone.
- Put the brief in "artifact": {"brief": "...", "acceptance": ["..."], "files": ["..."]}.
- If the work is one repo, "set" repo and ref and go to executing. If it spans repos, go to delegated with one child per repo, each carrying its own brief in description.
- If it needs a credential, a resource, spend, or a decision only a person can make, go to needs-human and say exactly what.`,
			"worker": `You are the worker. Deliver the brief in the artifact above, in this repository: edit and commit as you go, in the working tree you are in.
- Do NOT push, do not open a PR, do not look for credentials — you have none, and you need none. When you finish, the harness pushes your branch and opens the PR for you, with your "report" as its body: what you did, how you verified it, what you left out and why.
- Stay inside the brief. Things you notice but must not touch go in "followups".
- Run the acceptance checks before you finish; put the results in "artifact": {"checks": ["..."], "report": "..."}.
- Go to reviewing when the work is committed and checked. Go to needs-human only when you are genuinely blocked, saying what by.`,
			"reviewer": `You are the reviewer. Judge the worker's result against the brief and the project's goal, not against taste.
- Re-run the acceptance checks yourself. Read the diff. Check nothing outside the brief changed.
- Go to ready when it holds; "artifact": {"verdict": "ready", "checked": ["..."]}.
- Go back to executing with precise notes when it does not; "artifact": {"verdict": "back", "fix": ["..."]}.
- Go to needs-human when the brief itself was wrong or the result touches something only a person should decide.`,
			"closer": `You are the closer. The result is merged. Record it and tidy up.
- Confirm the merge landed (the PR is closed/merged); if not, go to done anyway and say so in "note".
- Write a short record in "artifact": {"summary": "...", "landed": "..."}.
- Everything the work surfaced that remains — loose ends, debt, questions — becomes "followups", one each, terse.`,
		},
	}
}
