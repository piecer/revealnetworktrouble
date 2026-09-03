.PHONY: test test-race vet frontend-deps web-test web-test-syntax run build \
	android-wrapper-verify android-env android-test android-lint android-assemble android-check \
	ci-inner ci ci-clean-archive release verify-release

test:
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

android-wrapper-verify:
	./scripts/verify-android-wrapper.sh

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
