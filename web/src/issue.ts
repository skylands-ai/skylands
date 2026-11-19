/**
 * Work started from an issue: a link to it in the command bar places the
 * work on the repository the issue is in, and the island that holds it
 * reads the issue with its own gh. Its text reaches the agent only in a
 * prompt the person reads and signs.
 */

/** An issue a prompt links to, as the citadel read the link. */
export interface IssueLink {
  host: string
  owner: string
  repo: string
  number: number
  /** The link, without anything after the issue's number. */
  url: string
  /** owner/repo#12 */
  ref: string
  /** The https URL its repository clones from. */
  cloneUrl: string
}

/** An issue as the island's gh read it. */
export interface Issue {
  url: string
  number: number
  title: string
  body: string
  /** "OPEN" or "CLOSED". */
  state: string
}

/** Whether text may link to an issue, so even on a terrace the citadel is asked what it links to. */
export function mayLinkIssue(text: string): boolean {
  return /https:\/\/\S+\/issues\/[1-9]/.test(text)
}

/** What the person wrote besides the issue's link, in whatever form they pasted it. */
export function wordsBesides(prompt: string, link: IssueLink): string {
  const escaped = link.url.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return prompt
    .replace(new RegExp(`${escaped}(?:[/?#]\\S*)?(?![\\w])`, 'gi'), ' ')
    .replace(/[ \t]+/g, ' ')
    .replace(/ *\n */g, '\n')
    .trim()
}

/**
 * The prompt the person reads and signs for work on an issue: what they
 * wrote besides its link, or an ask to resolve it, then the issue's name,
 * title, link, and body, as the island read it.
 */
export function issuePrompt(prompt: string, link: IssueLink, issue: Pick<Issue, 'title' | 'body'>): string {
  const words = wordsBesides(prompt, link) || 'Resolve this issue.'
  const body = issue.body.trim()
  return `${words}\n\nIssue ${link.ref}: ${issue.title.trim()}\n${link.url}${body ? `\n\n${body}` : ''}`
}
