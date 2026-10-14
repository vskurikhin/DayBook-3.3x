package su.svn.api.repository;

import jakarta.ws.rs.ProcessingException;
import jakarta.ws.rs.WebApplicationException;
import org.junit.jupiter.api.Test;

import javax.net.ssl.SSLHandshakeException;
import java.io.IOException;
import java.net.ConnectException;
import java.net.SocketTimeoutException;
import java.net.UnknownHostException;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ExecutionException;

import static org.junit.jupiter.api.Assertions.*;

class RemotePageFailureTest {
    @Test
    void classifiesAvailabilityErrorsAndKnownWrappers() {
        for (Throwable failure : new Throwable[]{new UnknownHostException(), new ConnectException(),
                new SocketTimeoutException(), new IOException(), new io.smallrye.mutiny.TimeoutException(),
                new java.util.concurrent.TimeoutException(), new java.net.http.HttpTimeoutException("timeout")}) {
            assertTrue(RemotePageFailure.isRecoverable(failure));
            assertTrue(RemotePageFailure.isRecoverable(new ProcessingException(new CompletionException(new ExecutionException(failure)))));
        }
    }

    @Test
    void statusRangeDoesNotSwallowClientErrors() {
        for (int status : new int[]{200, 399, 400, 401, 403, 404, 499, 600}) {
            assertFalse(RemotePageFailure.isRecoverable(new WebApplicationException(status)));
        }
        for (int status : new int[]{500, 502, 503, 599}) {
            assertTrue(RemotePageFailure.isRecoverable(new WebApplicationException(status)));
        }
    }

    @Test
    void tlsPayloadAndProgrammingFailuresAreFatal() {
        for (Throwable failure : new Throwable[]{new SSLHandshakeException("certificate"),
                new IllegalArgumentException("payload"), new NullPointerException(),
                new ProcessingException("no known cause"), new RuntimeException(new UnknownHostException())}) {
            assertFalse(RemotePageFailure.isRecoverable(failure));
            assertFalse(RemotePageFailure.isRecoverable(new ProcessingException(failure)));
        }
        assertFalse(RemotePageFailure.isRecoverable(null));
    }

    @Test
    void wrapperTraversalIsBoundedAndCycleSafe() {
        Throwable deep = new UnknownHostException();
        for (int depth = 0; depth < 65; depth++) deep = new ProcessingException(deep);
        assertFalse(RemotePageFailure.isRecoverable(deep));
        var first = new ProcessingException("first");
        var second = new ProcessingException(first);
        first.initCause(second);
        assertFalse(RemotePageFailure.isRecoverable(first));
    }
}
