package tutor

// Role is the persona a session runs as: a tutor who climbs a hint ladder one
// rung per request, or an interviewer who does not hint at all.
//
// It is part of a session's identity rather than a label on it. One person can
// have a tutoring conversation and an interview open on the same problem, and a
// session restored as the wrong one would start coaching in the middle of an
// interview.
type Role string

const (
	RoleTutor       Role = "tutor"
	RoleInterviewer Role = "interviewer"
)

func (r Role) Valid() bool { return r == RoleTutor || r == RoleInterviewer }

// Protocol is the document that is the whole of this role's instructions. Both
// live in tutor/ and both are canonical: the code sends one or the other, it
// does not paraphrase either.
func (r Role) Protocol() string {
	if r == RoleInterviewer {
		return "INTERVIEW.md"
	}
	return "TUTOR.md"
}

// Briefing is what a provider is told on top of the protocol: where its replies
// are rendered and how to pitch them.
//
// One function for both providers. The CLI reads the protocol off disk and the
// HTTP provider is handed it, but if each assembled its own briefing the two
// would drift into different interviewers on the same instance.
func (r Role) Briefing() string {
	common := "Your replies render as Markdown in a narrow side panel. Keep them short: " +
		"a few sentences, or a short list. Ask one question at a time and wait for the answer. " +
		"Reply in the person's language.\n"
	if r == RoleInterviewer {
		return common +
			"You are interviewing, not teaching. Do not give hints, do not name the pattern, " +
			"and do not suggest a data structure or an algorithm. You are told the pattern below " +
			"because you need it to judge the answer; never say it.\n" +
			"Ask for the approach first, then for the code. When they submit, you are shown the " +
			"code and the test output: if it works, ask one follow-up that complicates it — a " +
			"constraint, a change of scale, a variant. If it does not work, say what you observe " +
			"and ask what they make of it, without correcting it for them.\n" +
			"Each problem has a time limit the page keeps. When the page tells you the time is up, " +
			"say so once and offer to finish the problem or move on. Do not mention the clock " +
			"otherwise.\n" +
			"There may be a recorded hint ladder for this problem from their study. You are not " +
			"given it, and you must not ask for it."
	}
	return "The person is in a browser workspace with an editor and a Run-tests button, " +
		"so never tell them to run `nc start` or the test command — the page does both and logs the time and the rating.\n" +
		common +
		"Be warm and supportive: acknowledge a specific sound idea, effort, or self-correction " +
		"before the next question. Treat mistakes as something to explore together, not as a " +
		"judgement of ability. Avoid exaggerated praise and keep ratings accurate.\n" +
		"You cannot see their code unless it is included in the message you are answering. " +
		"If it is absent and you need it, say so rather than guessing."
}
