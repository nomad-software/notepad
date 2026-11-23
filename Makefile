BINARY := notepad
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

# -s -w leaves out the symbol table and debug information, which roughly
# halves the size of the binary. The version is the one Go records from
# git, which the About box shows.
LDFLAGS := -s -w

.PHONY: build run test bench vet check icons screenshots appimage clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: build
	./$(BINARY)

# Every run of the tests has a timeout, so that one that hangs fails with
# the stacks of what it was waiting on, rather than waiting for ever. The GUI
# tests run on Qt's offscreen platform, so they need no display.
TEST_TIMEOUT := 5m

test:
	go test -race -timeout $(TEST_TIMEOUT) ./...

# Times the editor on a 13 MB file: reading, writing, typing, finding and
# replacing. To see what a change did, save the output from before and
# after it and compare the two:
#
#   make bench COUNT=10 > old.txt    (then, with the change)
#   make bench COUNT=10 > new.txt
#   benchstat old.txt new.txt
#
# BENCH picks the benchmarks by a regular expression: make bench BENCH=Find.
BENCH ?= .
COUNT ?= 1

bench:
	go test -run '^$$' -bench '$(BENCH)' -benchmem -count $(COUNT) -timeout 30m ./...

vet:
	go vet ./...

# Everything the code is checked with before it is done.
check: vet
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "not formatted with gofmt:"; echo "$$out"; exit 1; fi
	staticcheck ./...
	go run github.com/nomad-software/qute/cmd/qute@$$(go list -m -f '{{.Version}}' github.com/nomad-software/qute) vet ./...

# Draws the program's icon, internal/appicon/appicon.png, from
# packaging/notepad.svg. Run it again whenever the SVG changes.
icons:
	rsvg-convert -w 256 -h 256 packaging/notepad.svg -o internal/appicon/appicon.png

# Saves the README's screenshot of the window, docs/screenshot.png.
screenshots:
	NOTEPAD_SCREENSHOTS=$(CURDIR)/docs go test -run "TestScreenshot" -count=1 ./internal/ui

# Builds Notepad-<version>-x86_64.AppImage here with the Qt found by
# pkg-config (see packaging/appimage.sh). The work is done in a temporary
# folder outside the project, which is removed afterwards. Releases are
# built by .github/workflows/appimage.yml on Ubuntu 22.04, so they run on
# older systems than one built here would.
appimage:
	VERSION=$(VERSION) packaging/appimage.sh

clean:
	rm -f $(BINARY) Notepad-*.AppImage
