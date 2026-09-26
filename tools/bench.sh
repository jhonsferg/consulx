#!/usr/bin/env bash
# Runs the ConsulX benchmarks locally. Benchmarks are behind the "bench"
# build tag, so CI never compiles or runs them; this script is the
# supported way to run them. See docs/benchmarks.md.
#
# Usage: tools/bench.sh [options]
#
#   -p PATTERN   benchmark regexp passed to -bench (default ".")
#   -c COUNT     runs per benchmark, 6 or more for benchstat (default 6)
#   -t TIME      -benchtime value, e.g. 1s or 1000x (default 1s)
#   -m MODULES   space-separated module directories (default: core and contrib)
#   -o FILE      result file (default .bench/<timestamp>.txt)
#   -b FILE      baseline result file; prints a benchstat comparison
#   -P PACKAGE   profile one package of the core module (e.g. ./balancer):
#                writes CPU and memory profiles to .bench/profiles and
#                prints the top functions
#   -L           run inside a Linux container (golang:1.27, needs Docker);
#                use it to profile on Windows, where -cpuprofile can freeze
#
# Examples:
#   tools/bench.sh -c 1 -t 200ms                    # quick smoke run
#   tools/bench.sh -o .bench/before.txt             # baseline before a change
#   tools/bench.sh -o .bench/after.txt -b .bench/before.txt
#   tools/bench.sh -L -P ./balancer -p NextStrategy # where does Next spend?
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

pattern=.
count=6
benchtime=1s
modules=". contrib/prometheus contrib/otel contrib/fiber"
out=""
baseline=""
profile=""
linux=false
image=golang:1.27

while getopts "p:c:t:m:o:b:P:Lh" opt; do
	case $opt in
	p) pattern=$OPTARG ;;
	c) count=$OPTARG ;;
	t) benchtime=$OPTARG ;;
	m) modules=$OPTARG ;;
	o) out=$OPTARG ;;
	b) baseline=$OPTARG ;;
	P) profile=$OPTARG ;;
	L) linux=true ;;
	h | *)
		sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	esac
done

mkdir -p .bench
flags=(-tags bench -run '^$' -bench "$pattern" -benchmem -count "$count" -benchtime "$benchtime")

# gotest DIR ARGS... runs "go test ARGS..." in module directory DIR, on the
# host or in the Linux container. Module and build caches live in named
# volumes, so only the first container run downloads dependencies.
gotest() {
	local dir=$1
	shift
	if ! $linux; then
		(cd "$dir" && go test "$@")
		return
	fi
	local host
	host=$(pwd -W 2>/dev/null || pwd) # Git Bash on Windows needs the Windows path
	MSYS_NO_PATHCONV=1 docker run --rm \
		-v "$host:/src" -v consulx-gomod:/go/pkg/mod -v consulx-gocache:/root/.cache/go-build \
		-w "/src/$dir" "$image" go test "$@"
}

benchstat_cmd() {
	if command -v benchstat >/dev/null 2>&1; then
		benchstat "$@"
	else
		go run golang.org/x/perf/cmd/benchstat@latest "$@"
	fi
}

if [[ -n $profile ]]; then
	dir=.bench/profiles
	mkdir -p "$dir"
	name=$(basename "$profile")
	# Profiles carry their symbols, so pprof needs no test binary; go test
	# leaves it in the working directory when profiling, so remove it.
	gotest . "${flags[@]}" -cpuprofile "$dir/$name.cpu" -memprofile "$dir/$name.mem" "$profile"
	rm -f "$name.test" "$name.test.exe"
	echo
	echo "== CPU: top functions =="
	go tool pprof -top -nodecount=25 "$dir/$name.cpu" 2>/dev/null | sed -n '1,32p'
	echo
	echo "== Memory: top allocation sites (bytes) =="
	go tool pprof -top -nodecount=25 -sample_index=alloc_space "$dir/$name.mem" 2>/dev/null | sed -n '1,32p'
	echo
	echo "Interactive views:"
	echo "  go tool pprof -http=: $dir/$name.cpu"
	echo "  go tool pprof -http=: -sample_index=alloc_space $dir/$name.mem"
	exit 0
fi

out=${out:-.bench/$(date +%Y%m%d-%H%M%S).txt}
mkdir -p "$(dirname "$out")"
: >"$out"
for m in $modules; do
	echo "== $m ==" >&2
	gotest "$m" "${flags[@]}" ./... | tee -a "$out"
done
cp "$out" .bench/latest.txt
echo >&2
echo "Results: $out (copied to .bench/latest.txt)" >&2

if [[ -n $baseline ]]; then
	echo
	benchstat_cmd "$baseline" "$out"
elif [[ $count -gt 1 ]]; then
	echo
	benchstat_cmd "$out"
fi
