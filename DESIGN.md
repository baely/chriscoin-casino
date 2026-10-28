# ChrisCoin Casino Design Draft

ChrisCoin Casino runs on GitHub Issues, Git, and GitHub Actions. Players deposit ChrisCoin into the operator's ChrisCoin account, wager through comments on their personal casino issue, and withdraw winnings back to their ChrisCoin account.

ChrisCoin remains authoritative for external transfers. A separate append-only casino ledger records player balances, fees, wagers, and withdrawals. All amounts are whole CC. There is no automatic casino starting allowance.

## Proposed rules

| Operation | Rule | Example |
| --- | --- | --- |
| Deposit | Credit the settled transfer amount minus 1 CC | Send 100 CC; receive 99 CC casino credit |
| Withdrawal | Deduct the requested amount; send that amount minus 1 CC | Withdraw 50 CC; receive 49 CC in ChrisCoin |
| Wager | Stake a positive whole number of CC; return between zero and twice the stake | Wager 10 CC; receive between 0 and 20 CC |
| House edge | Expected wager return is 95% of the stake | A 10 CC wager has an expected return of 9.5 CC |

The 1 CC fee applies once to each successful deposit or withdrawal. Wagers have no additional transfer fee. These fee semantics and the 5% house edge are proposed defaults. Reject withdrawals below 2 CC. A settled 1 CC deposit produces zero player credit and a 1 CC fee; display this before directing players to deposit.

## Player accounts and commands

A player opens their own issue; its authenticated opener is the account owner, identified by their numeric GitHub user ID. Record that original issue and opener in the ledger. The casino does not assign issues or create account issues for players. Assignees, labels, issue titles, and body text do not identify the owner. Deposits received before the player opens an issue stay credited to their numeric GitHub ID and are available when they open their first issue. Concurrent attempts must resolve to the same original issue.

Accept these comments on the recorded account issue:

```text
/deposit 100
/wager 10
/withdraw 50
/balance
```

The original issue remains the user's account even when closed. A new duplicate issue receives a link to the original and is closed; processing that new duplicate reopens the original. Persist duplicate completion so later scheduled recovery, comments, deposits, or reopening an old duplicate cannot reopen the original again.

Every original issue receives welcome instructions covering commands, deposit options, fees, odds, and account ownership. Reject all comments by another user with a link to their own account issue or the account creation template. Duplicate issues accept no transactions.

Only the account owner's authenticated numeric ID can submit its financial commands. Reject commands on pull requests, other issues, or another player's account issue. Do not authorize a command using a username, issue label, assignment, or a sender field supplied in its text.

Each financial command is identified by the casino repository ID and GitHub comment ID. Freeze the authenticated author, command, body hash, and game version when accepting it. A command executes at most once; editing or deleting a comment cannot change an accepted command. Ignore edit events, and reject comments modified before acceptance when detected. After acceptance, recovery uses the ledger even if the issue is closed or the comment disappears.

Bot replies show the outcome, resulting available balance, and ledger commit or PR. Retrying a failed reply must not repeat the underlying transaction.

## Deposits

Support both casino-created payment requests and direct incoming transfers. `/deposit N` creates a treasury-authored ChrisCoin payment request and returns a Pay link after canonical request creation. The depositor must authorize fulfillment themselves. A request can settle only once; credit follows the actual payer even if that differs from the requested payer name. Direct transfers require no prior request and are credited independently, with a fee for each settled payment. Creating a request does not itself credit or reserve funds.

Initialize the operator's ChrisCoin account before accepting deposits. Existing recipients receive direct payments in the current ChrisCoin implementation. Configure the treasury using its numeric GitHub user ID and the exact upstream repository identity.

Use the operator's personal account `baely`, numeric GitHub ID `5674656`, as the treasury. The fixed upstream starting commit is `fc9a755a24bfedd28dd018a35f958d3be3a62aca`, selected after pulling ChrisCoin. Its 18 ledger entries leave 1,067 available CC and zero reserved CC in the treasury. Treat that existing balance as opening house capital and import settled payments after the baseline, including those that arrive before the casino's first live run. Preserve the configured baseline during initialization rather than advancing it to the launch-time upstream head.

For each reconciliation run:

1. Read ChrisCoin's canonical `ledger/events.jsonl` at a specific `main` commit and validate its accounting. Verify that the previously imported ledger is still an unchanged prefix; stop reconciliation on an unexpected rewrite.
2. Identify newly settled transfers whose actual recipient is the treasury's numeric ID. For direct payments, the sender is the event actor. For accepted offers and paid requests, derive the sender and recipient by replaying the referenced transaction.
3. Attribute the deposit to the sender's numeric ID. Do not credit pending offers, payment request creation, screenshots, or bot comments.
4. Append a casino deposit event with the upstream repository, payment identity, commit, gross amount, fee, and player credit.
5. Advance the import checkpoint in the same canonical commit as those deposit events.

A settled upstream payment can be credited only once, including when it is visible through both its original offer and its settlement event. Exclude treasury self-transfers. Record operator capital separately so it does not also create a player liability.

## Withdrawals

Withdrawals cross two independent Git ledgers. Use a durable sequence of states rather than assuming both repositories can update atomically:

```text
requested -> funds locked -> upstream issue recorded -> payment confirmed
                                               \-> confirmed failure -> funds released
```

First commit a withdrawal request that moves its gross amount from available to locked player funds. Assign a unique withdrawal ID derived from the original casino comment. Then create a ChrisCoin transfer issue authenticated as the operator, sending the net amount to the player. Include the withdrawal ID in an immutable local record and an upstream issue marker for reconciliation.

Resolve the destination against the player's numeric ID. Require the upstream transaction to settle as a direct payment to that ID; do not accept an open, claimable offer as a completed withdrawal. If a username change prevents a safe direct payment under ChrisCoin's current account snapshots, leave the withdrawal pending until the identity mapping is resolved.

Confirm payment from ChrisCoin's canonical ledger, checking the sender, recipient, amount, and recorded upstream issue. Only then remove the locked liability and recognize the 1 CC withdrawal fee.

If issue creation times out, its success is uncertain. Reconcile against upstream issues and the ledger before sending again. If the result remains ambiguous, leave funds locked and flag the withdrawal for operator resolution. Never blindly create a replacement transfer. Release funds only after confirming the original transfer cannot still settle.

### Authentication requirement

ChrisCoin currently authorizes the issue author as the payer and rejects accounts whose GitHub type is not `User`. An issue opened by the casino's Actions bot therefore cannot spend the operator's ChrisCoin balance.

Use a classic personal access token owned by `baely` with `public_repo` scope, stored as the casino Actions secret `CHRISCOIN_TOKEN`. The upstream repository belongs to another personal account, so a fine-grained PAT cannot write to it under the current access arrangement. The casino's ordinary `GITHUB_TOKEN` handles its own ledger PRs and replies; its permissions are limited to the casino repository.

An alternative is an explicit delegated-spending feature in ChrisCoin. That requires an upstream change and is outside the initial casino implementation.

## Wagers and randomness

There is no configured maximum wager. Accept any positive whole stake covered by the player's available balance and the house's maximum possible loss, subject to exact integer arithmetic.

For a stake `W`, use this distribution:

```text
With probability 5%:
    payout = 0
Otherwise:
    payout = a uniform integer from 0 through 2 * W inclusive
```

The uniform component has mean `W`, giving an overall expected payout of `0.95 * W`. Payout includes the stake: a 10 CC wager returning 17 CC produces a 7 CC profit. Whole-coin sampling avoids fractional payout rounding and preserves the stated expectation for every permitted stake.

Use Go's `crypto/rand` with unbiased bounded sampling. Do not derive randomness from timestamps, commit hashes, or public comment IDs.

Commit wager acceptance before generating or revealing the draw. Acceptance reserves the player's stake and the house's maximum possible net loss. Persist the generated draw on a durable request checkpoint before publishing it, then commit settlement. Once a draw is durable, every retry reuses it. Recovery must settle accepted wagers even if their source comments are later edited or deleted.

Record the game version, stake, draw, and payout so replay can validate settlement arithmetic. Rule changes apply to future accepted wagers. A public ledger makes results auditable, but does not independently prove that the operator generated randomness honestly; independently verifiable randomness would require a further protocol.

## Accounting and house reserve

Replay the casino ledger to derive available player balances, locked withdrawal amounts, reserved wagers, fees, and house capital. Persist events as the source of truth; generated balance snapshots are disposable.

Track treasury CC against player liabilities. Before accepting a wager, the house must cover the largest possible increase in player liabilities: a stake of `W` can produce a net player gain of `W`. Reserve that house exposure until settlement, so multiple accepted wagers cannot commit the same capital.

For example, a new 100 CC deposit creates a 99 CC player liability and only 1 CC of free house capital. It does not by itself support a 10 CC wager with a possible 20 CC payout. Seed a house bankroll before opening wagering.

Pending withdrawals remain liabilities until upstream payment confirmation. Reconcile outgoing treasury payments against those liabilities before treating a lower treasury balance as a shortfall. Keep imported treasury balances and confirmation events consistent at the same upstream checkpoint. Pause new wagering or withdrawals when an unexplained shortfall is detected.

Player funds cannot become negative, a payment cannot be imported twice, and a wager cannot settle twice. Check amount limits and arithmetic overflow, including `2 * W`, before accepting commands. Operator withdrawals of profits must leave player liabilities and reserved wager exposure fully funded.

## Ledger and processor

Use `ledger/events.jsonl` for canonical casino events. Event types include account creation, deposit credit, wager acceptance, wager settlement, withdrawal request, withdrawal submission, withdrawal completion, withdrawal release, and operator capital movements.

Each event records its version, sequence, unique ID, authenticated actor, source issue and comment where applicable, timestamp, and relevant upstream references. Financial events explicitly record amounts and fees. Replaying accepted events must not depend on mutable issue text or current game settings.

Follow ChrisCoin's processor pattern: load current `main`, validate the next transition, create or recover a deterministic request branch, validate the complete proposed ledger, and merge the exact bot PR head. Recover bot branches after interruption. Never automatically merge arbitrary participant-authored ledger changes.

## GitHub Actions

Run the worker for account request issues, newly created comments, manual dispatch, and scheduled recovery:

```yaml
on:
  issues:
    types: [opened]
  issue_comment:
    types: [created]
  schedule:
    - cron: "*/5 * * * *"
  workflow_dispatch:

concurrency:
  group: casino-ledger
  cancel-in-progress: false
```

All casino ledger writers share this lock. Each run reconciles upstream transfers, drains unprocessed account commands, and recovers unfinished wagers and withdrawals. It must scan beyond the triggering comment because default Actions concurrency can replace pending runs. Recovery must include recorded unfinished operations on closed issues. Use pagination and bounded batches, advancing checkpoints only after successful canonical commits.

Actions schedules can be delayed, and public repository schedules can be disabled after inactivity. Settlement is asynchronous; provide manual dispatch for recovery. Do not rely on bot commits or comments automatically triggering another workflow. Keep recovery and any optional Pages deployment under appropriate serialization so older state cannot replace newer output.

Run write-enabled jobs using trusted code from `main`. Parse event data through the GitHub event file or API; never interpolate comment bodies into shell scripts. Keep participant code and fork PRs away from the treasury token. Ordinary players need public read and issue access, not repository write access.

## Suggested implementation

Use Go, matching ChrisCoin's existing structure:

```text
cmd/casino/             CLI commands for processing and validation
internal/ledger/       Event parsing, replay, balances, and invariants
internal/game/         Versioned payout rules and bounded random sampling
internal/processor/    Command acceptance, PRs, settlement, and recovery
internal/github/       Repository API operations and authenticated identities
internal/chriscoin/    Upstream ledger replay and transfer reconciliation
ledger/events.jsonl   Canonical casino ledger
.github/workflows/    Worker and read-only PR validation
```

ChrisCoin's current Go packages are under `internal/`, so a separate casino module cannot import them directly. Initially port the necessary ledger reader and validation code with its upstream license and attribution, and pin its behavior to the supported upstream event version. A shared upstream library can follow if both projects need it. Do not copy ChrisCoin's automatic starting allowance into casino accounting.

Before enabling the hosted flow, verify replay and fee arithmetic, deposit deduplication, account ownership, bankroll limits, frozen wager outcomes, and recovery around every withdrawal boundary. Simulate API success followed by a lost response, repeated Actions delivery, and upstream payment confirmation followed by a failed casino commit. Exercise deposits, wagering, and withdrawals with two real GitHub accounts.

## References

- [ChrisCoin implementation and ledger](https://github.com/cdlewis/ChrisCoin)
- [GitHub Actions token scope](https://docs.github.com/en/actions/concepts/security/github_token)
- [GitHub issue comment events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#issue_comment)
- [GitHub Actions concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)
- [GitHub scheduled workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule)
