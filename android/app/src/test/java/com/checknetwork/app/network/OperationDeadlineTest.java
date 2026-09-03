package com.checknetwork.app.network;

import static org.junit.Assert.assertEquals;

import java.util.concurrent.TimeUnit;
import org.junit.Test;

public final class OperationDeadlineTest {
    @Test public void subMillisecondElapsedTimeUsesCeilingRemainingMilliseconds() {
        FakeClock clock = new FakeClock(100L);
        OperationDeadline deadline = new OperationDeadline(clock, 1_000L);

        clock.nanos = 101L;
        assertEquals(1_000L, deadline.remainingMillis());
        clock.nanos = 100L + 999_999L;
        assertEquals(1_000L, deadline.remainingMillis());
        clock.nanos = 100L + 1_000_000L;
        assertEquals(999L, deadline.remainingMillis());
    }

    @Test public void exactBoundaryExpires() {
        FakeClock clock = new FakeClock(7L);
        OperationDeadline deadline = new OperationDeadline(clock, 315_000L);

        clock.nanos = 7L + TimeUnit.MILLISECONDS.toNanos(315_000L);

        assertEquals(0L, deadline.remainingMillis());
    }

    @Test public void regressionAndWrapCannotIncreaseRemainingTime() {
        FakeClock clock = new FakeClock(1_000L);
        OperationDeadline deadline = new OperationDeadline(clock, 100L);
        clock.nanos = 1_000L + TimeUnit.MILLISECONDS.toNanos(10L);
        assertEquals(90L, deadline.remainingMillis());

        clock.nanos = 999L;
        assertEquals(90L, deadline.remainingMillis());

        clock.nanos = Long.MAX_VALUE - 500_000L;
        OperationDeadline wrapping = new OperationDeadline(clock, 2L);
        clock.nanos = Long.MIN_VALUE + 499_999L;
        assertEquals(1L, wrapping.remainingMillis());

        clock.nanos = Long.MIN_VALUE;
        OperationDeadline overflowing = new OperationDeadline(clock, 2L);
        clock.nanos = Long.MAX_VALUE;
        assertEquals(0L, overflowing.remainingMillis());
    }

    @Test public void hugeBudgetUsesSaturatingArithmetic() {
        FakeClock clock = new FakeClock(0L);
        OperationDeadline deadline = new OperationDeadline(clock, Long.MAX_VALUE);
        clock.nanos = TimeUnit.MILLISECONDS.toNanos(1L);
        assertEquals(Long.MAX_VALUE - 1L, deadline.remainingMillis());
    }

    private static final class FakeClock implements OperationDeadline.Clock {
        long nanos;
        FakeClock(long nanos) { this.nanos = nanos; }
        @Override public long nanoTime() { return nanos; }
    }
}
