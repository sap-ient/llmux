package to.llmux;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

import java.io.IOException;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.net.HttpURLConnection;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * JUnit tests for the llmux Java sidecar launcher.
 *
 * <p>The launcher resolves the binary from the {@code LLMUX_BINARY} environment
 * variable, which a unit JVM cannot mutate in-process. So the fixture-driven
 * tests (spawn / readiness / singleton / cleanup) are gated on {@code
 * LLMUX_BINARY} being set by the runner (Surefire {@code <environmentVariables>}
 * in CI, or the {@code run-java-check.sh} wrapper). The pure unit tests
 * (URL formatting, the {@code waitHealthy} health-poll helper, and the missing
 * binary error) always run.
 */
class LlmuxTest {

    @BeforeEach
    @AfterEach
    void reset() {
        Llmux.stop();
    }

    // --- helpers -----------------------------------------------------------

    /** A binary the launcher would actually spawn: env override, else bundled. */
    private static String resolvableBinary() {
        String env = System.getenv("LLMUX_BINARY");
        if (env != null && !env.isEmpty()) {
            return env;
        }
        Path bundled = Path.of("bin", "llmux");
        return Files.isRegularFile(bundled) ? bundled.toAbsolutePath().toString() : null;
    }

    private static boolean portOpen(int port) {
        try (Socket s = new Socket()) {
            s.connect(new InetSocketAddress("127.0.0.1", port), 300);
            return true;
        } catch (IOException e) {
            return false;
        }
    }

    private static boolean waitPortClosed(int port, Duration timeout) {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (!portOpen(port)) {
                return true;
            }
            try {
                Thread.sleep(50);
            } catch (InterruptedException ignored) {
                Thread.currentThread().interrupt();
            }
        }
        return !portOpen(port);
    }

    private static int healthStatus(String base) throws IOException {
        HttpURLConnection c =
                (HttpURLConnection) URI.create(base + "/health").toURL().openConnection();
        c.setConnectTimeout(1000);
        c.setReadTimeout(1000);
        int code = c.getResponseCode();
        c.disconnect();
        return code;
    }

    private static int portOf(String base) {
        return Integer.parseInt(base.substring(base.lastIndexOf(':') + 1));
    }

    // --- URL formatting + readiness + singleton + cleanup ------------------
    //
    // Gated on a spawnable binary (a fake fixture or the real gateway) being
    // reachable via LLMUX_BINARY / bundled bin.

    @Test
    void startReadinessUrlSingletonAndCleanup() throws Exception {
        String bin = resolvableBinary();
        assumeTrue(bin != null, "set LLMUX_BINARY to a fake/real binary to run this test");

        String base = Llmux.start(null);
        assertTrue(base.matches("http://127\\.0\\.0\\.1:\\d+"), base);

        // URL formatting.
        assertEquals(base + "/v1", Llmux.openaiBaseUrl());
        assertTrue(Llmux.openaiBaseUrl().endsWith("/v1"));

        // Readiness: /health is 200.
        assertEquals(200, healthStatus(base));

        // Singleton: second start returns same base, no respawn.
        int port = portOf(base);
        assertEquals(base, Llmux.start(null));
        assertEquals(base, Llmux.baseUrl());
        assertTrue(portOpen(port));

        // Cleanup: stop() kills the child and frees the port.
        Llmux.stop();
        assertTrue(waitPortClosed(port, Duration.ofSeconds(3)), "port not freed after stop");
    }

    @Test
    void timesOutWhenHealthNeverSucceeds() throws Exception {
        // A health server that never returns 200 must cause start() to fail. We
        // construct it with a real local server and exercise the private
        // waitHealthy poll directly (no binary needed).
        com.sun.net.httpserver.HttpServer srv =
                com.sun.net.httpserver.HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        srv.createContext("/health", ex -> {
            ex.sendResponseHeaders(503, -1);
            ex.close();
        });
        srv.start();
        try {
            String base = "http://127.0.0.1:" + srv.getAddress().getPort();
            Method m = Llmux.class.getDeclaredMethod("waitHealthy", String.class, Duration.class);
            m.setAccessible(true);
            InvocationTargetException ex = assertThrows(
                    InvocationTargetException.class,
                    () -> m.invoke(null, base, Duration.ofMillis(400)));
            assertTrue(ex.getCause() instanceof LlmuxException);
            assertTrue(ex.getCause().getMessage().contains("did not become healthy"));
        } finally {
            srv.stop(0);
        }
    }

    // --- health-poll helper directly --------------------------------------

    @Test
    void waitHealthyBecomesReadyOn200() throws Exception {
        com.sun.net.httpserver.HttpServer srv =
                com.sun.net.httpserver.HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        srv.createContext("/health", ex -> {
            ex.sendResponseHeaders(200, -1);
            ex.close();
        });
        srv.start();
        try {
            String base = "http://127.0.0.1:" + srv.getAddress().getPort();
            Method m = Llmux.class.getDeclaredMethod("waitHealthy", String.class, Duration.class);
            m.setAccessible(true);
            m.invoke(null, base, Duration.ofSeconds(3)); // should not throw
        } finally {
            srv.stop(0);
        }
    }

    @Test
    void waitHealthyTimesOutWhenUnreachable() throws Exception {
        // Reserve then release a port so nothing is listening.
        int port;
        try (java.net.ServerSocket s = new java.net.ServerSocket()) {
            s.bind(new InetSocketAddress("127.0.0.1", 0));
            port = s.getLocalPort();
        }
        String base = "http://127.0.0.1:" + port;
        Method m = Llmux.class.getDeclaredMethod("waitHealthy", String.class, Duration.class);
        m.setAccessible(true);
        assertThrows(InvocationTargetException.class,
                () -> m.invoke(null, base, Duration.ofMillis(400)));
    }

    // --- binary resolution: clear error when missing ----------------------

    @Test
    void binaryPathClearErrorWhenMissing() throws Exception {
        // Only meaningful when nothing is resolvable in this environment.
        String override = System.getenv("LLMUX_BINARY");
        boolean bundled = Files.isRegularFile(Path.of("bin", "llmux"));
        boolean onPath = whichLlmux();
        assumeTrue(
                (override == null || override.isEmpty()) && !bundled && !onPath,
                "an llmux binary is resolvable here; cannot assert the not-found path");
        Method m = Llmux.class.getDeclaredMethod("binaryPath");
        m.setAccessible(true);
        InvocationTargetException e =
                assertThrows(InvocationTargetException.class, () -> m.invoke(null));
        assertTrue(e.getCause() instanceof LlmuxException);
        assertTrue(e.getCause().getMessage().contains("llmux binary not found"));
    }

    private static boolean whichLlmux() {
        String path = System.getenv("PATH");
        if (path == null) {
            return false;
        }
        for (String dir : path.split(java.io.File.pathSeparator)) {
            if (Files.isExecutable(Path.of(dir, "llmux"))) {
                return true;
            }
        }
        return false;
    }

    // --- integration (real binary) ----------------------------------------

    @Test
    void integrationRealBinary() throws Exception {
        String real = System.getenv("LLMUX_BINARY");
        Path bundled = Path.of("bin", "llmux");
        String bin = (real != null && !real.isEmpty())
                ? real
                : (Files.isRegularFile(bundled) ? bundled.toAbsolutePath().toString() : null);
        assumeTrue(bin != null, "real llmux binary not available");

        Llmux.Options o = new Llmux.Options();
        o.timeout = Duration.ofSeconds(15);
        String base = Llmux.start(o);
        assertTrue(base.matches("http://127\\.0\\.0\\.1:\\d+"));
        assertEquals(200, healthStatus(base));
        assertTrue(Llmux.openaiBaseUrl().endsWith("/v1"));
    }
}
