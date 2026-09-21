> ### Opening this build the first time
>
> These artefacts are **not signed with an Apple Developer ID and not notarized**, so the first
> launch of each version is refused by the operating system. Nothing is wrong with the download —
> the macOS bundle carries an ad-hoc signature that verifies intact, and every artefact here has its
> `.sha256` beside it.
>
> **macOS** — "Apple could not verify that CodeFlow.app is free of malware":
> open **System Settings → Privacy & Security**, scroll to the message naming CodeFlow and click
> **Open Anyway**. The button is offered for about an hour after the attempt, and the app is
> remembered from then on. ([Apple's own instructions](https://support.apple.com/en-us/102445).)
>
> **Windows** — "Windows protected your PC": click **More info → Run anyway**.
>
> Both warnings say the same thing: the publisher paid no certificate authority. They are not a
> verdict about the file.
