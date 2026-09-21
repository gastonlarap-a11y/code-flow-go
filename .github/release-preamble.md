> ### Installing on macOS
>
> One command, and the warning below never appears:
>
> ```sh
> curl -fsSL https://raw.githubusercontent.com/gastonlarap-a11y/code-flow-go/main/scripts/install-macos.sh | bash
> ```
>
> It downloads this release, checks it against the `.sha256` published beside it, and copies
> `CodeFlow.app` into `/Applications`. Already downloaded the `.dmg` from this page? Point the same
> script at it instead of fetching it again — it is verified the same way:
>
> ```sh
> curl -fsSL https://raw.githubusercontent.com/gastonlarap-a11y/code-flow-go/main/scripts/install-macos.sh \
>   | CODEFLOW_DMG=~/Downloads/CodeFlow-{{VERSION}}-arm64.dmg bash -s -- v{{VERSION}}
> ```
>
> ### Opening a build downloaded from this page
>
> These artefacts are **not signed with an Apple Developer ID and not notarized**, so a copy
> downloaded through a browser is refused by the operating system. Nothing is wrong with the
> download — the macOS bundle carries an ad-hoc signature that verifies intact, and every artefact
> here has its `.sha256` beside it.
>
> **What decides it is how the file reached the machine, not which version it is.** The refusal
> comes from `com.apple.quarantine`, an attribute browsers write and `curl` does not, and Gatekeeper
> evaluates each bundle it is set on. So: a `.dmg` **downloaded from this page with a browser** is
> refused **every time, on every version** — a new version is a new bundle to evaluate. An update
> the app fetches through its own updater carries no such flag and installs without a word, which is
> why nobody updating in place ever sees this. 2.7.x was exactly the same.
>
> **macOS** — "Apple could not verify that CodeFlow.app is free of malware":
> open **System Settings → Privacy & Security**, scroll to the message naming CodeFlow and click
> **Open Anyway**. The button is offered for about an hour after the attempt, and that copy of the
> app is remembered from then on — the next version you download here is not.
> ([Apple's own instructions](https://support.apple.com/en-us/102445).) The install command above
> avoids the dialog altogether, for this version and every one after it.
>
> **Windows** — "Windows protected your PC": click **More info → Run anyway**.
>
> Both warnings say the same thing: the publisher paid no certificate authority. They are not a
> verdict about the file.
