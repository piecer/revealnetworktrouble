package com.checknetwork.app;

import static org.junit.Assert.assertFalse;

import org.junit.Test;

public class JvmCollectionSmokeTest {
    @Test
    public void junitCollectsAndRunsPlainJvmTests() {
        assertFalse(System.getProperty("java.version").isBlank());
    }
}
