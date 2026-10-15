/*
 * This file was last modified at 2026.10.14 15:47 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RemotePageFailure.java
 * $Id$
 */

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
import java.util.Objects;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ExecutionException;

/** Classifies only availability failures at the Core page boundary. */
public final class RemotePageFailure {

    public static final int MAX_DEPTH = 64;
    public static final int MIN_SERVER_ERROR_STATUS = 500;
    public static final int MAX_SERVER_ERROR_STATUS = 599;

    private RemotePageFailure() {
    }

    /**
     * Determines whether a remote page failure permits fallback to local data.
     * Recognizes HTTP 5xx and availability failures, unwrapping only
     * {@link ProcessingException}, {@link CompletionException}, and {@link ExecutionException}.
     * The first recognized failure determines the result; SSL failures are not recoverable.
     *
     * @param failure the failure to inspect; may be {@code null}
     * @return {@code true} for a recoverable failure; {@code false} for null,
     *         unsupported failures, cycles, or exhaustion of {@link #MAX_DEPTH}
     */
    public static boolean isRecoverable(Throwable failure) {
        if (Objects.isNull(failure)) {
            return false;
        }
        var visited = Collections.newSetFromMap(new IdentityHashMap<Throwable, Boolean>());
        for (int depth = 0; failure != null && depth < MAX_DEPTH && visited.add(failure); depth++) {
            if (failure instanceof SSLException) {
                return false;
            }
            if (failure instanceof WebApplicationException response) {
                return isServerError(response);
            }
            if (isAvailabilityFailure(failure)) {
                return true;
            }
            if (isUnsupportedWrapper(failure)) {
                return false;
            }
            failure = failure.getCause();
        }
        return false;
    }

    private static boolean isServerError(WebApplicationException response) {
        return response.getResponse() != null
                && response.getResponse().getStatus() >= MIN_SERVER_ERROR_STATUS
                && response.getResponse().getStatus() <= MAX_SERVER_ERROR_STATUS;
    }

    private static boolean isAvailabilityFailure(Throwable failure) {
        return failure instanceof UnknownHostException
                || failure instanceof SocketException
                || failure instanceof SocketTimeoutException
                || failure instanceof TimeoutException
                || failure instanceof java.util.concurrent.TimeoutException
                || failure instanceof java.net.http.HttpTimeoutException
                || failure.getClass() == IOException.class;
    }

    private static boolean isUnsupportedWrapper(Throwable failure) {
        return !(failure instanceof ProcessingException
                || failure instanceof CompletionException
                || failure instanceof ExecutionException);
    }
}
