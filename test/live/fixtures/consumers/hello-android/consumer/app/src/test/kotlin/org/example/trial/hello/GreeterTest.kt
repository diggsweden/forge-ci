// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later
package org.example.trial.hello

import org.junit.Assert.assertEquals
import org.junit.Test

class GreeterTest {
    @Test
    fun trimsNames() {
        assertEquals("Hello, Forge CI!", Greeter.greet(" Forge CI "))
    }

    @Test
    fun handlesMissingNames() {
        assertEquals("Hello, world!", Greeter.greet("  "))
        assertEquals("Hello, world!", Greeter.greet(null))
    }
}
