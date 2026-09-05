package github

// GraphQL documents (storage §S.4.1). Every query selects rateLimit so the
// collector can back off before the hourly budget is spent.

// queryContributions is Q1: commits per day per repository (commits only),
// the contribution calendar (mixed events), followers and the years with
// activity. maxRepositories: 100 is the API maximum (review protocol-09);
// truncation is detected through totalRepositoriesWithContributedCommits.
const queryContributions = `query Contributions($login: String!, $from: DateTime!, $to: DateTime!) {
  viewer { login }
  user(login: $login) {
    followers { totalCount }
    contributionsCollection(from: $from, to: $to) {
      contributionYears
      totalCommitContributions
      totalRepositoriesWithContributedCommits
      restrictedContributionsCount
      hasAnyRestrictedContributions
      contributionCalendar {
        totalContributions
        weeks { contributionDays { date contributionCount } }
      }
      commitContributionsByRepository(maxRepositories: 100) {
        repository { nameWithOwner isPrivate owner { login } }
        contributions(first: 100, orderBy: { field: OCCURRED_AT, direction: ASC }) {
          totalCount
          pageInfo { hasNextPage endCursor }
          nodes { occurredAt commitCount }
        }
      }
    }
  }
  rateLimit { cost remaining resetAt }
}`

// queryMergedPRs is Q2: merged pull requests by the login, 100 per page.
const queryMergedPRs = `query MergedPRs($q: String!, $after: String) {
  search(type: ISSUE, query: $q, first: 100, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        mergedAt
        repository { name isPrivate owner { login } }
      }
    }
  }
  rateLimit { cost remaining resetAt }
}`

// queryOpenPRs is Q3: the count of open pull requests outside the login's own repositories.
const queryOpenPRs = `query OpenPRs($q: String!) {
  search(type: ISSUE, query: $q, first: 1) { issueCount }
  rateLimit { cost remaining resetAt }
}`

// queryRepos is Q4: public, non-fork repositories owned by the login with their star counts.
const queryRepos = `query Repos($login: String!, $after: String) {
  user(login: $login) {
    repositories(first: 100, after: $after, ownerAffiliations: [OWNER], privacy: PUBLIC, isFork: false,
                 orderBy: { field: STARGAZERS, direction: DESC }) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes { name stargazerCount isArchived }
    }
  }
  rateLimit { cost remaining resetAt }
}`

// ---- response shapes (docs.github.com/graphql object references) ----

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type contributionsData struct {
	Viewer struct {
		Login string `json:"login"`
	} `json:"viewer"`
	User *struct {
		Followers struct {
			TotalCount int `json:"totalCount"`
		} `json:"followers"`
		ContributionsCollection struct {
			ContributionYears                       []int `json:"contributionYears"`
			TotalCommitContributions                int   `json:"totalCommitContributions"`
			TotalRepositoriesWithContributedCommits int   `json:"totalRepositoriesWithContributedCommits"`
			RestrictedContributionsCount            int   `json:"restrictedContributionsCount"`
			HasAnyRestrictedContributions           bool  `json:"hasAnyRestrictedContributions"`
			ContributionCalendar                    struct {
				TotalContributions int `json:"totalContributions"`
				Weeks              []struct {
					ContributionDays []struct {
						Date              string `json:"date"`
						ContributionCount int    `json:"contributionCount"`
					} `json:"contributionDays"`
				} `json:"weeks"`
			} `json:"contributionCalendar"`
			CommitContributionsByRepository []struct {
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
					IsPrivate     bool   `json:"isPrivate"`
					Owner         struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"repository"`
				Contributions struct {
					TotalCount int      `json:"totalCount"`
					PageInfo   pageInfo `json:"pageInfo"`
					Nodes      []struct {
						OccurredAt  string `json:"occurredAt"`
						CommitCount int    `json:"commitCount"`
					} `json:"nodes"`
				} `json:"contributions"`
			} `json:"commitContributionsByRepository"`
		} `json:"contributionsCollection"`
	} `json:"user"`
}

type mergedPRsData struct {
	Search struct {
		IssueCount int      `json:"issueCount"`
		PageInfo   pageInfo `json:"pageInfo"`
		Nodes      []struct {
			MergedAt   string `json:"mergedAt"`
			Repository struct {
				Name      string `json:"name"`
				IsPrivate bool   `json:"isPrivate"`
				Owner     struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		} `json:"nodes"`
	} `json:"search"`
}

type openPRsData struct {
	Search struct {
		IssueCount int `json:"issueCount"`
	} `json:"search"`
}

type reposData struct {
	User *struct {
		Repositories struct {
			TotalCount int      `json:"totalCount"`
			PageInfo   pageInfo `json:"pageInfo"`
			Nodes      []struct {
				Name           string `json:"name"`
				StargazerCount int    `json:"stargazerCount"`
				IsArchived     bool   `json:"isArchived"`
			} `json:"nodes"`
		} `json:"repositories"`
	} `json:"user"`
}
