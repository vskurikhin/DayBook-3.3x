package su.svn.api.repository;

import io.smallrye.mutiny.TimeoutException;
import jakarta.ws.rs.ProcessingException;
import jakarta.ws.rs.WebApplicationException;

import javax.net.ssl.SSLException;
import java.io.IOException;
import java.net.SocketException;
import java.net.SocketTimeoutException;
import java.net.UnknownHostException;
import java.util.Collections;
import java.util.IdentityHashMap;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ExecutionException;

/** Classifies only availability failures at the Core page boundary. */
public final class RemotePageFailure {
    private RemotePageFailure() {
    }

    public static boolean isRecoverable(Throwable failure) {
        var visited = Collections.newSetFromMap(new IdentityHashMap<Throwable, Boolean>());
        for (int depth = 0; failure != null && depth < 64 && visited.add(failure); depth++) {
            if (failure instanceof SSLException) {
                return false;
            }
            if (failure instanceof WebApplicationException response) {
                return response.getResponse() != null && response.getResponse().getStatus() >= 500
                        && response.getResponse().getStatus() <= 599;
            }
            if (failure instanceof UnknownHostException || failure instanceof SocketException
                    || failure instanceof SocketTimeoutException || failure instanceof TimeoutException
                    || failure instanceof java.util.concurrent.TimeoutException
                    || failure instanceof java.net.http.HttpTimeoutException
                    || failure.getClass() == IOException.class) {
                return true;
            }
            if (!(failure instanceof ProcessingException || failure instanceof CompletionException
                    || failure instanceof ExecutionException)) {
                return false;
            }
            failure = failure.getCause();
        }
        return false;
    }
}
