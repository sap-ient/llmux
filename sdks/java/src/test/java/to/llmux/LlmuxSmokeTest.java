package to.llmux;

import java.lang.reflect.Method;
import java.net.HttpURLConnection;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URI;
import java.time.Duration;

/**
 * Dependency-free runnable check for the Java SDK (no JUnit / Maven needed).
 *
 * <p>Compile + run with:
 * <pre>
 *   javac -d out src/main/java/to/llmux/*.java src/test/java/to/llmux/LlmuxSmokeTest.java
 *   LLMUX_BINARY=&lt;fake-or-real&gt; java -cp out to.llmux.LlmuxSmokeTest
 * </pre>
 *
 * <p>Asserts: URL formatting, /health readiness, singleton (no respawn),
 * cleanup (port freed), and the waitHealthy timeout on a never-200 server.
 * Exits non-zero on the first failure.
 */
public final class LlmuxSmokeTest {

    private static int checks;

    public static void main(String[] args) throws Exception {
        // waitHealthy timeout: works without any binary.
        testWaitHealthyTimesOut();

        String bin = System.getenv("LLMUX_BINARY");
        if (bin == null || bin.isEmpty()) {
            System.out.println("LLMUX_BINARY not set; ran " + checks
                    + " check(s). Set it to a fake/real binary for the full smoke test.");
            return;
        }

        String base = Llmux.start(null);
        check(base.matches("http://127\\.0\\.0\\.1:\\d+"), "base url shape: " + base);
        check(Llmux.openaiBaseUrl().equals(base + "/v1"), "openaiBaseUrl == base + /v1");
        check(Llmux.openaiBaseUrl().endsWith("/v1"), "openaiBaseUrl ends with /v1");
        check(healthStatus(base) == 200, "health is 200");

        int port = Integer.parseInt(base.substring(base.lastIndexOf(':') + 1));
        check(Llmux.start(null).equals(base), "singleton: same base on second start");
        check(Llmux.baseUrl().equals(base), "singleton: baseUrl matches");
        check(portOpen(port), "port open while running");

        Llmux.stop();
        check(waitPortClosed(port, Duration.ofSeconds(3)), "port freed after stop");

        System.out.println("OK: " + checks + " checks passed");
    }

    private static void testWaitHealthyTimesOut() throws Exception {
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
            boolean threw = false;
            try {
                m.invoke(null, base, Duration.ofMillis(400));
            } catch (java.lang.reflect.InvocationTargetException e) {
                threw = e.getCause() instanceof LlmuxException;
            }
            check(threw, "waitHealthy times out on never-200");
        } finally {
            srv.stop(0);
        }
    }

    private static int healthStatus(String base) throws Exception {
        HttpURLConnection c =
                (HttpURLConnection) URI.create(base + "/health").toURL().openConnection();
        c.setConnectTimeout(1000);
        c.setReadTimeout(1000);
        int code = c.getResponseCode();
        c.disconnect();
        return code;
    }

    private static boolean portOpen(int port) {
        try (Socket s = new Socket()) {
            s.connect(new InetSocketAddress("127.0.0.1", port), 300);
            return true;
        } catch (Exception e) {
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

    private static void check(boolean cond, String label) {
        checks++;
        if (!cond) {
            System.err.println("FAIL: " + label);
            System.exit(1);
        }
        System.out.println("ok: " + label);
    }

    private LlmuxSmokeTest() {}
}
