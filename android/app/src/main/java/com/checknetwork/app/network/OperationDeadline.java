package com.checknetwork.app.network;

import java.util.Objects;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;

/**
 * One absolute operation budget measured from construction by a single monotonic clock.
 * Remaining time is rounded up to milliseconds and can never increase, even if the clock regresses.
 */
public final class OperationDeadline {
    public interface Clock { long nanoTime(); }

    private final Clock clock;
    private final long startedNanos;
    private final long budgetMillis;
    private final AtomicLong remainingCeilingMillis;

    public OperationDeadline(Clock clock, long budgetMillis) {
        this.clock = Objects.requireNonNull(clock, "clock");
        this.budgetMillis = Math.max(0L, budgetMillis);
        this.remainingCeilingMillis = new AtomicLong(this.budgetMillis);
        this.startedNanos = clock.nanoTime();
    }

    /** Returns a thread-safe, nonincreasing, ceiling-rounded millisecond budget. */
    public long remainingMillis() {
        long measured = measuredRemainingMillis(clock.nanoTime());
        return remainingCeilingMillis.updateAndGet(previous -> Math.min(previous, measured));
    }

    Clock clock() { return clock; }

    private long measuredRemainingMillis(long nowNanos) {
        if (budgetMillis == 0L || nowNanos == startedNanos) return budgetMillis;
        long elapsedNanos = nowNanos - startedNanos;
        if (elapsedNanos < 0L) {
            // A lower reading is a regression unless signed subtraction proves a normal nanoTime wrap.
            return nowNanos < startedNanos ? budgetMillis : 0L;
        }
        long elapsedMillis = TimeUnit.NANOSECONDS.toMillis(elapsedNanos);
        if (elapsedMillis >= budgetMillis) return 0L;
        return budgetMillis - elapsedMillis;
    }
}
