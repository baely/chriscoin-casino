# ChrisCoin Casino

A Go worker that runs a ChrisCoin casino through GitHub Issues, Git, and GitHub Actions. Git stores the ledger; GitHub accounts identify players; Actions processes commands. No server or database is required.

This is a working implementation draft. Automated tests exercise the complete flow against simulated GitHub and ChrisCoin stores; hosted multi-account operation still needs verification after configuration.

## Playing

Open an issue using **Open casino account** to get your personal account and welcome instructions. Use `/deposit N` there to receive a ChrisCoin payment link. Paying that link is the only way to add chips. Whoever pays it receives the credit after settlement, minus 1 CC. If the payer has no casino issue yet, the credit stays against their numeric GitHub ID until they open one. CC sent straight to the treasury account in [ChrisCoin](https://github.com/cdlewis/ChrisCoin) is never credited to a player; it is a tip for the house. The casino page links a prefilled 10 CC tip.

Comment on your account issue:

```text
/deposit 100
/wager 10
/withdraw 50
/balance
```

The issue opener is the account owner, identified by their numeric GitHub ID. The casino does not assign issues or create account issues on a player's behalf. Assignees, labels, titles, and body text do not determine the user. Each user has one permanent account issue. Opening a second issue closes it with a link to the original and reopens the original if needed. Closing your account issue does not delete it or move its balance. The worker only reopens it when handling a new duplicate issue; scheduled recovery, comments, deposits, and reopening an already handled duplicate do not reopen the original.

Comments on another player's issue are rejected with a link to the commenter's own issue, or a link to open one. This applies to plain comments as well as commands. Commands on duplicate issues redirect to the original account. Each original issue receives a welcome message explaining commands, fees, payouts, and ownership. The welcome message and your latest command reply always show your current available balance. Each older reply shows your balance as it stood just before your next command, rebuilt from the ledger, so it never changes once a newer reply exists. Its status still updates, for example when a bet settles or a deposit is paid.

Only that account's numeric GitHub user ID can submit its commands. Renaming your GitHub account does not transfer ownership. Each comment executes at most once; submit a new comment for another wager. Edited comments are rejected unless a command was already frozen on its bot request branch. An accepted wager remains binding after its comment is edited or deleted.

| Operation | Result |
| --- | --- |
| Deposit 100 CC | 99 CC added to your casino balance; 1 CC fee |
| Withdraw 50 CC | 50 CC removed from your balance; 49 CC sent to your ChrisCoin account; 1 CC fee |
| Wager 10 CC | 10 CC staked; return between 0 and 20 CC, including the stake |

Withdrawals require at least 2 CC. A 1 CC deposit is entirely consumed by the deposit fee. Casino accounts start at zero; ChrisCoin's upstream starting allowance is never duplicated inside the casino.

### Operator commands

The treasury account can comment these on a player's account issue:

```text
/credit 5 sorry about the outage
/freeze spamming other players
/unfreeze
```

`/credit N reason` adds N CC to the player's balance from the house, even if that takes the house balance negative. `/freeze reason` pauses the player's wagers and `/deposit` requests. `/withdraw` and `/balance` keep working, so a frozen balance always stays the player's. Requests the player already received can still be paid and credited. `/unfreeze` lifts the freeze. Every operator command is a public ledger event with its reason, and the player sees the reason in the reply. The treasury's comments without a slash on player issues are ignored, so the operator can chat with a player there. No command takes funds from a player or changes a wager result.

The treasury account cannot deposit into itself: a self-payment moves no CC and creates no player credit or deposit fee. Use a separate GitHub account to play when you also operate the treasury.

Wagers return whole CC between zero and twice the stake, including the stake. Go's `crypto/rand` generates the draw; the ledger records each accepted wager and its settlement.

### Roulette and slots

```text
/roulette 10 red
/roulette 5 17
/slots 10
```

Roulette is single-zero European roulette, and every bet returns 36/37 of the stake on average. Bet `red`, `black`, `odd`, `even`, `low` (or `1-18`), or `high` (or `19-36`) to win twice the stake. Bet `1-12`, `13-24`, or `25-36` to win three times the stake, or a single number from `0` to `36` to win 36 times the stake. Zero loses every outside bet.

Slots spin three reels over one 20-stop strip: one 7️⃣, two ⭐, four 🔔, six 🍋, and seven 🍒. Three of a kind pays 50x for 7️⃣, 25x for ⭐, 12x for 🔔, 8x for 🍋, or 3x for 🍒. Exactly two 🍒 anywhere pay 2x. The exact expected return is 7597/8000 (about 95%), and about 32% of spins pay something.

Payouts include the stake. Draws are frozen on their bot branch exactly like `/wager` draws, so retries never reroll.

## Setup

The checked-in configuration uses `cdlewis/ChrisCoin`, treasury account `baely` (GitHub ID `5674656`), and fixed starting commit `fc9a755a24bfedd28dd018a35f958d3be3a62aca`. ChrisCoin was pulled before selecting that commit. Its 18 ledger entries replay to 1,067 available CC and zero reserved CC for the treasury at the baseline.

There is no maximum wager. A player can stake any positive whole amount covered by their available balance, and every bet is honoured in full whatever the house holds. The house balance (treasury minus everything owed to players) can go negative. Exact integer accounting and overflow checks still apply.

Withdrawals are the only place the treasury's real funds are checked. A withdrawal is refused if the treasury's ChrisCoin balance cannot pay it after every withdrawal already queued, so cash-outs are paid first come, first served. A refused withdrawal leaves the balance untouched, and the player can try a smaller amount or try again later. If the treasury drops after a withdrawal was accepted, ChrisCoin rejects the payment and the casino returns the coins without a fee.

Complete these GitHub steps:

1. Create a public `baely/chriscoin-casino` repository and publish this local checkout to its `main` branch. Keep Issues and Actions enabled. The local checkout currently has no GitHub remote configured.
2. In **Settings → Actions → General**, enable **Allow GitHub Actions to create and approve pull requests**. Permit squash merging under the repository's pull request settings. The worker validates and merges its exact ledger PR head; required external CI checks or human reviews on those bot PRs prevent automatic settlement. Keep ordinary participants at public read access.
3. While signed in as `baely`, create a **personal access token (classic)** with the **`public_repo`** scope and an expiry you can rotate before it lapses. Store it in the casino repository under **Settings → Secrets and variables → Actions** as **`CHRISCOIN_TOKEN`**. ChrisCoin belongs to `cdlewis`; fine-grained PATs cannot write to an unrelated public repository in this access arrangement. This token creates deposit requests and outgoing withdrawal issues as `baely`. Do not commit the token to Git.
4. Manually run **Casino ledger** from the repository's Actions tab. The worker initializes from the fixed starting commit and immediately imports any subsequent settled payments. Then verify a small deposit, wager, and withdrawal with two real GitHub accounts.

No new ChrisCoin account is needed. The 1,067 CC already held by `baely` at the baseline becomes opening house capital; transfers recorded at or before that baseline do not become player deposits. Payments to the treasury that are not for a casino deposit request are kept as house income. Keep the personal account funded so players can cash out.

The initial configuration is recorded in the ledger. Changing the treasury, upstream repository, or starting commit after initialization requires an explicit migration; the worker refuses a configuration mismatch. The ordinary `GITHUB_TOKEN` is supplied automatically by Actions and needs no manually created secret.

Without `CHRISCOIN_TOKEN`, wagers still work, but no deposits are possible. `/deposit` requests and withdrawals remain pending until a correctly authenticated token is configured; withdrawals keep their funds locked.

GitHub references: [repository Actions permissions](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository), [personal access token limitations](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens), and [public repository token scope](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps).

## Local checks

Requires Go 1.26 or newer. The implementation uses the Go standard library and has no module dependencies.

```sh
go test -race ./...
go vet ./...
go run ./cmd/casino validate
go run ./cmd/casino state
```

`validate` replays the ledger and checks accounting. `state` prints the rebuilt account, wager, withdrawal, and treasury state. To verify an existing prefix is preserved:

```sh
go run ./cmd/casino validate --base-ledger /path/to/previous-events.jsonl
```

The live `process` command requires `GITHUB_REPOSITORY` and `GITHUB_TOKEN`; it optionally accepts `CHRISCOIN_TOKEN`. It writes through the GitHub API, not the local checkout. Run it through the workflow, where every ledger writer shares the same concurrency lock. Use the in-memory tests for local transaction processing instead of running a second live writer.

## Settlement and recovery

The canonical casino state is `ledger/events.jsonl` on `main`. Each mutation is validated by replay, frozen on a deterministic bot branch, and merged through a PR. Participants' hand-written ledger PRs are never processed automatically. The PR validation workflow has a read-only token and no treasury secret.

Every run reconciles the upstream ledger, handles account issues and commands, and recovers recorded operations. Pagination includes closed account issues so closing an issue cannot discard an accepted wager. A five-minute scheduled recovery handles coalesced triggers and interrupted runs; Actions scheduling is best-effort, so use manual dispatch if necessary. The first successful canonical merge establishes processing order, rather than the time a comment was submitted.

A `/deposit N` command first records the intent and creates a treasury-authored ChrisCoin payment request. Its reply shows a prefilled Pay link once the request appears in ChrisCoin's canonical ledger. The player submits the fulfillment issue while authenticated as themselves; the casino never gets their token. Retrying payment against the same request cannot pay it twice. Upstream issues use ChrisCoin's standard titles and fenced JSON commands, with an opaque hidden reference for retry recovery. If request creation loses its API response, recovery checks treasury-authored references before creating another request. Creating a request does not transfer or reserve player funds.

Only payments of the casino's own deposit requests are reconciled into balances. Direct transfers, accepted offers, and requests the treasury created by hand are tips to the house. Every run links recovered deposit requests before importing ChrisCoin payments, so a request found after a lost API response is still credited when paid. Because ChrisCoin allows another account to fulfill a named payer's request, credit always follows the authenticated actual payer.

Deposits are credited only after upstream settlement. Upstream repository identity, the imported prefix hash, settled payer and recipient IDs, and unique payment IDs prevent forged receipts and repeated credits. Personal treasury spending reduces what players can withdraw. Fee totals are accounting records, not an automated transfer to a separate wallet.

Wager acceptance locks the player's stake before sampling. Once the draw reaches its request branch, retries reuse it. A failed reply or a lost merge response cannot reroll a wager.

Withdrawals lock the gross amount, record submission intent, and then create the upstream issue. Completion requires a matching canonical direct payment to the player's numeric ID. A terminal rejection recorded by the ChrisCoin bot releases the locked amount without charging a withdrawal fee. Merely closing an upstream issue is not sufficient proof of failure.

If the upstream issue creation response is lost, recovery searches treasury-authored issues for the withdrawal marker and links the original issue. The worker never blindly creates a replacement payment. If execution stopped between committing submission intent and posting the issue, or the original issue cannot be identified, the draft leaves funds locked for operator reconciliation. Inspect both ledgers and the workflow run before any manual correction. There is no automated abandon or resend command.

A stale or reused upstream username pauses a withdrawal rather than paying a different numeric ID or creating an open claimable offer. Refresh the account's username through ChrisCoin before retrying. Unexpected upstream history rewrites stop imports.

## Code

- `cmd/casino`: processing, validation, and state inspection commands.
- `internal/ledger`: casino events, strict command parsing, replay, fees, liabilities, and bankroll checks.
- `internal/game`: versioned whole-coin payout rules and random sampling.
- `internal/processor`: authenticated account issues, command handling, PR checkpoints, upstream reconciliation, and recovery.
- `internal/github`: repository REST client and exact-head PR merging.
- `internal/chriscoin`: upstream v1 ledger replay and settled transfer recognition.
- `internal/site`: the static casino page built by `go run ./cmd/casino site`.

The upstream replay and GitHub client are adapted from ChrisCoin under its MIT license; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). [DESIGN.md](DESIGN.md) contains the original design draft. The implementation uses player-opened account issues, duplicate issue recovery, ownership redirects, welcome messages, and request-only deposits.
