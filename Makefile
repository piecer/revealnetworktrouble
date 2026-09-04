define _derive_java_home
normalize_absolute_path() ( \
	path_to_normalize=$$1; normalized=; rest=$${path_to_normalize#/}; \
	while [ -n "$$rest" ]; do \
		case $$rest in */*) component=$${rest%%/*}; rest=$${rest#*/} ;; *) component=$$rest; rest= ;; esac; \
		case $$component in ''|.) ;; ..) case $$normalized in */*) normalized=$${normalized%/*} ;; *) normalized= ;; esac ;; *) normalized=$${normalized:+$$normalized/}$$component ;; esac; \
	done; \
	printf '/%s\n' "$$normalized"; \
); \
javac_path=$$(command -v javac 2>/dev/null) || exit 0; \
case $$javac_path in /*) ;; *) javac_path=$$(normalize_absolute_path "$$PWD/$$javac_path") || exit 0 ;; esac; \
resolved=$$(normalize_absolute_path "$$javac_path") || exit 0; seen=; hops=0; \
while [ -L "$$resolved" ] || [ -h "$$resolved" ]; do \
	case "
$$seen
" in *"
$$resolved
"*) exit 0 ;; esac; \
	seen=$${seen}$${seen:+"
"}$$resolved; \
	[ "$$hops" -lt 40 ] || exit 0; \
	target=$$(readlink "$$resolved" 2>/dev/null) || exit 0; \
	[ -n "$$target" ] || exit 0; \
	case $$target in /*) next=$$target ;; *) parent=$${resolved%/*}; next=$$parent/$$target ;; esac; \
	resolved=$$(normalize_absolute_path "$$next") || exit 0; hops=$$((hops + 1)); \
done; \
[ -f "$$resolved" ] && [ -x "$$resolved" ] || exit 0; \
parent=$${resolved%/*}; normalize_absolute_path "$$parent/.."
endef

ifeq ($(origin JAVA_HOME),undefined)
JAVA_HOME := $(shell $(_derive_java_home))
endif
export JAVA_HOME

_android_home_origin := $(origin ANDROID_HOME)
_android_sdk_root_origin := $(origin ANDROID_SDK_ROOT)
_android_standard_sdk := $(HOME)/Android/Sdk
_android_managed_sdk := $(HOME)/.local/share/checknetwork-android/sdk
ifneq ($(_android_home_origin),undefined)
_android_sdk := $(ANDROID_HOME)
else ifneq ($(_android_sdk_root_origin),undefined)
_android_sdk := $(ANDROID_SDK_ROOT)
else
_android_sdk := $(shell for sdk in "$(_android_standard_sdk)" "$(_android_managed_sdk)"; do if [ -f "$$sdk/platforms/android-35/android.jar" ] && [ -x "$$sdk/build-tools/35.0.0/aapt2" ]; then printf '%s\n' "$$sdk"; break; fi; done)
endif
override ANDROID_HOME := $(_android_sdk)
override ANDROID_SDK_ROOT := $(_android_sdk)
export ANDROID_HOME ANDROID_SDK_ROOT

REVISION ?= $(shell git rev-parse HEAD 2>/dev/null)

.PHONY: test test-race vet frontend-deps web-test web-test-syntax run build \
	android-wrapper-verify android-env-bootstrap-test android-env android-test android-lint \
	android-assemble android-check ci-inner ci ci-clean-archive release verify-release \
	verify-release-real verify-api-archive-real verify-web-archive-real verify-archives-real

test: android-env-bootstrap-test
	go test ./...
	$(MAKE) web-test

frontend-deps:
	npm --prefix frontend ci
	@printf 'CI_OK: frontend-deps\n'

web-test:
	npm --prefix frontend test

web-test-syntax:
	npm --prefix frontend run test:syntax

test-race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/checknetwork-api

build:
	go build -trimpath -o bin/checknetwork-api ./cmd/checknetwork-api

release:
	CHECKNETWORK_RELEASE_OUTPUT=bin/checknetwork-api ./scripts/verify-release.sh "$$(git rev-parse HEAD)"

verify-release:
	./scripts/verify-release.sh "$$(git rev-parse HEAD)"

verify-release-real:
	./scripts/verify_release_real_test.sh "$(REVISION)"

verify-api-archive-real:
	CHECKNETWORK_RUN_REAL_BUILDX_TESTS=1 go test ./cmd/checknetwork-api -run '^TestAPIOfflineArchiveRealBuildx$$' -count=1 -v

verify-web-archive-real:
	CHECKNETWORK_RUN_REAL_BUILDX_TESTS=1 go test ./cmd/checknetwork-api -run '^TestWebOfflineArchiveRealBuildx$$' -count=1 -v

verify-archives-real: verify-api-archive-real verify-web-archive-real

android-wrapper-verify:
	./scripts/verify-android-wrapper.sh

android-env-bootstrap-test:
	./scripts/test-android-env-bootstrap.sh

android-env:
	./scripts/verify-android-env.sh

android-test: android-wrapper-verify android-env
	rm -rf android/app/build/test-results/testDebugUnitTest android/app/build/test-results/testReleaseUnitTest
	cd android && ./gradlew --no-daemon --console=plain :app:testDebugUnitTest :app:testReleaseUnitTest
	./scripts/assert-android-test-results.sh \
		android/app/build/test-results/testDebugUnitTest \
		android/app/build/test-results/testReleaseUnitTest

android-lint: android-wrapper-verify android-env
	cd android && ./gradlew --no-daemon --console=plain :app:lintDebug :app:lintRelease

android-assemble: android-wrapper-verify android-env
	cd android && ./gradlew --no-daemon --console=plain :app:assembleDebug :app:assembleRelease

android-check: android-test android-lint android-assemble

ci-inner: frontend-deps
	./scripts/ci-inner.sh

ci: ci-inner

ci-clean-archive:
	./scripts/ci-clean-archive.sh "$$(git rev-parse HEAD)"
