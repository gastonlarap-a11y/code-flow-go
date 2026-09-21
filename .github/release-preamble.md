> ### Opening a build downloaded from this page
>
> These artefacts are **not signed with an Apple Developer ID and not notarized**, so a copy
> downloaded through a browser is refused once by the operating system. Nothing is wrong with the
> download — the macOS bundle carries an ad-hoc signature that verifies intact, and every artefact
> here has its `.sha256` beside it.
>
> **This is a one-time cost, not a per-version one.** What the browser adds is a quarantine flag;
> an update the app downloads through its own updater carries none, so it installs without a word.
> 2.7.x was exactly the same — ad-hoc signed, refused by Gatekeeper if you fetched it here — which
> is why nobody updating in place ever saw this.
>
> **macOS** — "Apple could not verify that CodeFlow.app is free of malware":
> open **System Settings → Privacy & Security**, scroll to the message naming CodeFlow and click
> **Open Anyway**. The button is offered for about an hour after the attempt, and the app is
> remembered from then on. ([Apple's own instructions](https://support.apple.com/en-us/102445).)
> Downloading with `curl` instead of a browser sets no quarantine flag and skips the dialog
> entirely.
>
> **Windows** — "Windows protected your PC": click **More info → Run anyway**.
>
> Both warnings say the same thing: the publisher paid no certificate authority. They are not a
> verdict about the file.
