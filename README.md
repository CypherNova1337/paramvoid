# Paramvoid

A fast, resilient HTTP **parameter discovery** tool. Point it at a URL (or a
list of them) and it finds the hidden query/body parameters a server quietly
accepts but never documents — the kind that lead to IDORs, hidden debug modes,
mass-assignment, and access-control bugs.

Single static Go binary. No runtime, no interpreter, no dependencies. It ships
with its own wordlist, so it works the moment you build it.

## Credit where it's due

The core idea here — establish a response baseline, then binary-search a
wordlist to find the parameters a server *reacts* to — comes from
[**Arjun**](https://github.com/s0md3v/Arjun) by [s0md3v](https://github.com/s0md3v).
Arjun is a great tool and I've leaned on it for years. paramvoid isn't a fork or
a port of its code; it's an independent reimplementation in Go that keeps the
detection technique I trust and rebuilds everything around it the way I actually
want it to behave on real, long-running engagements:

- **It doesn't die on you.** Rate limits, flaky hosts, and dodgy responses slow
  it down or get retried — they don't crash the run and cost you a two-hour scan.
- **It adapts to rate limiting instead of tripping over it.** When a target
  starts throttling, paramvoid backs off automatically, retries the exact same
  parameters, and ramps back up once the host recovers.
- **It resumes.** Big scans checkpoint to disk, so a `Ctrl-C`, a crash, or a
  dropped connection means *continue*, not *start over*.
- **It's self-contained.** Bundled wordlist, one consolidated output file, and
  URL auto-correction so a messy recon list feeds straight in.

Same instinct, built for the way I work. Full credit to s0md3v for the original.

## Install

```bash
go install github.com/CypherNova1337/paramvoid@latest
```

Or build from source:

```bash
git clone https://github.com/CypherNova1337/paramvoid
cd paramvoid
go build -o paramvoid .
```

Requires Go 1.21+. That's the only prerequisite.

## Usage

```bash
# Single URL, built-in wordlist
paramvoid -u https://api.target.tld/endpoint

# A list of targets (URLs auto-corrected, deduped), resumable, results to a file
paramvoid -i targets.txt --state scan.state -oT found.txt

# POST JSON body, through Burp, conservative rate
paramvoid -u https://api.target.tld/v1/users -m JSON --rate-limit 8 -oB 127.0.0.1:8080

# Resume an interrupted scan
paramvoid -i targets.txt --state scan.state --resume
```

### What a run looks like

```
[~] Scanning 1/1: https://api.target.tld/endpoint
[~] Probing the target for stability
[*] Analysing HTTP response for anomalies
[+] Extracted 3 parameters from response for testing
[~] Logicforcing the endpoint
[!] Processing chunks: 6/25
[<] parameter detected: user_id (based on: body-length)
[+] Parameters found: user_id, debug, redirect
```

When a target starts throttling you'll see it adjust live rather than error out:

```
[!] Rate limiting detected — backing off, now ~4.0 req/s (auto-adjusting, not crashing)
[*] Target looks healthy — ramping back up to ~5.0 req/s
```

## Wordlist

paramvoid embeds a ~4,300-word parameter list in the binary, so `-w` is optional
— out of the box it just works. The same list is in this repo at
[`wordlists/default.txt`](wordlists/default.txt) if you want to read or extend it.

To use your own list, pass any file:

```bash
paramvoid -u https://target.tld/api -w /path/to/your/params.txt
```

One name per line; lines starting with `#` are ignored.

## Output & URL handling

- **One consolidated file, always.** Every URL's results go into a single file
  keyed by URL — never one file per target. If you don't pass `-o`/`-oT`, it
  writes `paramvoid_output.json` in the working directory by default, so a bulk
  scan never ends with results only on screen. The file is rewritten after each
  URL completes, so partial results survive a `Ctrl-C` or crash.
- **URLs are auto-corrected.** Both `-u` and every line of a `-i` list get
  cleaned before scanning: whitespace/quotes trimmed, `https://` prepended when
  a scheme is missing (a bare `example.com` works), malformed lines skipped with
  a count, and duplicates removed. A messy recon list feeds straight in.

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-u` | — | Target URL |
| `-i` | — | File of target URLs (one per line) |
| `-w` | *(built-in)* | Wordlist path; omit to use the embedded list |
| `-m` | `GET` | Method: `GET` / `POST` / `JSON` / `XML` |
| `-t` | `15` | Concurrent workers (forced to 1 with `--stable` or `-d`) |
| `-d` | `0` | Fixed delay between requests (seconds) |
| `-T` | `15` | Per-request timeout (seconds) |
| `-c` | auto | Chunk size (params per request) |
| `-o` / `-oJ` | — | JSON output file |
| `-oT` | — | Text output file (`url<TAB>method<TAB>param`) |
| `-oB` | — | Route through a proxy, e.g. `127.0.0.1:8080` |
| `--headers` | — | Extra headers, newline- or `\n`-separated |
| `--include` | — | Params sent with every request, e.g. `a=b&c=d` |
| `--disable-redirects` | off | Don't follow redirects |
| `--stable` | off | Prefer stability over speed (single worker) |
| `--casing` | — | Rewrite params to a style: `like_this` / `likeThis` / `likethis` |
| `--rate-limit` | `20` | Starting/maximum requests per second |
| `--rate-min` | `1` | Floor the adaptive limiter won't drop below |
| `--no-adapt` | off | Disable adaptive adjustment (fixed rate) |
| `--rate-codes` | `429,503` | HTTP codes treated as "slow down" |
| `--state` | — | Checkpoint file (enables resume) |
| `--resume` | off | Resume from `--state` |
| `--retries` | `3` | Transient network-error retries per request |
| `-q` | off | Quiet mode |

## How it works

1. **Stability probe** — one plain request to confirm the target is reachable.
2. **Baseline & factors** — two requests with throwaway junk params fingerprint
   a "normal" response: status code, body length, word/line counts, redirect
   target, a filtered header set, and whether random values reflect in the body.
   Only signals that agree across both baselines are trusted.
3. **Factor stabilization** — extra junk requests prune any signal that
   fluctuates on its own, so dynamic pages don't generate false positives.
4. **Heuristic seeding** — parameter-like names already visible in the response
   (form/input names, JSON keys) are tested first.
5. **Narrowing** — the wordlist is chunked and each chunk sent at once. A chunk
   whose response deviates from the baseline contains a real parameter and is
   split in half; a chunk with no deviation is discarded. Binary search over the
   whole list converges fast.
6. **Verification** — every candidate is re-tested individually (twice) before
   it's reported, to drop flukes.

All HTTP goes through one adaptive client, so steps 2–6 never have to think
about rate limiting — a throttling target just makes them slower, not fatal.

## Note

- Resuming mid-target re-derives the response baseline on restart (it depends on
  live server behaviour), then continues the saved chunk queue.

## Legal

For authorized security testing and bug-bounty work only. You are responsible
for making sure you have permission to test any target you point this at.

## License

MIT — see [LICENSE](LICENSE).

---

Built by [CypherNova1337](https://github.com/CypherNova1337) / VoidSec.
