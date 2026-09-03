package com.checknetwork.app.network;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckCapabilities;
import org.junit.Test;

public final class TransportExceptionTest {
    @Test public void unsupportedCapabilityRequiresAndExposesOnlyClosedReason() {
        for (CheckCapabilities.CapabilityMismatchException.Reason reason
                : CheckCapabilities.CapabilityMismatchException.Reason.values()) {
            TransportException error = TransportException.unsupportedCapability(reason);
            assertEquals(TransportException.Kind.UNSUPPORTED_CAPABILITY, error.kind());
            assertEquals(reason, error.capabilityMismatchReason().orElseThrow());
            assertFalse(error.apiError().isPresent());
            assertEquals("The report request is not supported by the server capabilities.", error.getMessage());
            assertEquals("TransportException{kind=UNSUPPORTED_CAPABILITY, capabilityMismatchReason="
                    + reason + "}", error.toString());
        }
        assertThrows(NullPointerException.class, () -> TransportException.unsupportedCapability(null));
        assertThrows(IllegalArgumentException.class,
                () -> TransportException.of(TransportException.Kind.UNSUPPORTED_CAPABILITY));
        assertThrows(IllegalArgumentException.class,
                () -> new TransportException(TransportException.Kind.UNSUPPORTED_CAPABILITY, "unsafe"));
        assertThrows(IllegalArgumentException.class,
                () -> new TransportException(TransportException.Kind.API, "unsafe"));
    }

    @Test public void nonCapabilityKindsNeverCarryCapabilityReasonAndKindsRemainExhaustive() {
        assertArrayEquals(new TransportException.Kind[]{
                TransportException.Kind.NETWORK,
                TransportException.Kind.TIMEOUT,
                TransportException.Kind.CANCELLED,
                TransportException.Kind.RESPONSE_TOO_LARGE,
                TransportException.Kind.INVALID_RESPONSE,
                TransportException.Kind.API,
                TransportException.Kind.UNSUPPORTED_CAPABILITY}, TransportException.Kind.values());
        for (TransportException.Kind kind : TransportException.Kind.values()) {
            if (kind == TransportException.Kind.API || kind == TransportException.Kind.UNSUPPORTED_CAPABILITY) continue;
            assertFalse(TransportException.of(kind).capabilityMismatchReason().isPresent());
        }
    }
}