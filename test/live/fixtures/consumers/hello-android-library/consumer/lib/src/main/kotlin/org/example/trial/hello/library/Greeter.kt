// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later
package org.example.trial.hello.library

object Greeter {
    fun greet(name: String?): String {
        val trimmed = name?.trim().orEmpty()
        return "Hello, ${trimmed.ifEmpty { "world" }}!"
    }
}
