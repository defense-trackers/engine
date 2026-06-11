# Engine tasks. The CI workflows call `go` directly; this is for local use.
# On Windows without `make`, run the underlying `go` commands shown here.

.PHONY: build test golden fetch sentinel verify refresh-fixture clean

build:
	go build -o bin/engine .

test:
	go test ./...

# Regenerate the pagediff golden after an intentional parser/fixture change.
golden:
	go test ./fetchers/pagediff -run Golden -update

# --out points at a local checkout of the public site repo (sibling dir).
fetch: build
	./bin/engine fetch --out ../site

sentinel: build
	./bin/engine sentinel --out ../site

verify: build
	./bin/engine verify --out ../site

# Replace the synthetic fixture with the live Blue UAS page, then run `make golden`.
refresh-fixture:
	curl -sL https://www.diu.mil/blue-uas -o fetchers/pagediff/testdata/blue_uas_fixture.html

clean:
	rm -rf bin cache quarantine
