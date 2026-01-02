# ggdu

... a disk usage analyzer, like gdu, for Google Driver.

**Experimental** Feel free to try it out. I've created this for personal usage after the native drive utilities didn't help. Suggestions/PRs welcome.

## Requirements

- [gdrive](https://github.com/glotlabs/gdrive) must be installed and configured

You can test your configuration by manually running:

```
gdrive files list
```

If you see the files in your drive, you are good to go.

## Installation

```
go mod tidy
go install ./ggdu.go
```

## Usage

```
ggdu
```

Will open a TUI with your drive. 

Please remember that the analysis is cached (so we don't have to hog the API the whole time) in a local JSON file.

## Legal

- Copyright 2026 Christian Dominik Richter
- SPDX-License-Identifier: GPL-3.0-or-later

