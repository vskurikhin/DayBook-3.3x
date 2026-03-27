/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * GlobalExceptionHandler.java
 * $Id$
 */

package su.svn.core.services.excepthandler;

import lombok.extern.slf4j.Slf4j;
import org.springframework.data.crossstore.ChangeSetPersister;
import org.springframework.http.HttpStatus;
import org.springframework.http.converter.HttpMessageNotReadableException;
import org.springframework.web.bind.MethodArgumentNotValidException;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.context.request.WebRequest;
import su.svn.core.models.dto.ErrorResponse;

import java.util.stream.Collectors;
/**
 * Global exception handler for REST controllers.
 *
 * <p>This class intercepts exceptions thrown by controllers and converts them
 * into standardized {@link su.svn.core.models.dto.ErrorResponse} objects.</p>
 *
 * <p>Handles common application exceptions such as:</p>
 * <ul>
 *     <li>Entity not found</li>
 *     <li>Validation errors</li>
 *     <li>Malformed JSON / request body</li>
 *     <li>Unexpected server errors</li>
 * </ul>
 *
 * <p>Annotated with {@code @RestControllerAdvice} to apply globally.</p>
 */
@Slf4j
@RestControllerAdvice
public class GlobalExceptionHandler {
    @ResponseStatus(HttpStatus.NOT_FOUND)
    @ExceptionHandler(ChangeSetPersister.NotFoundException.class)
    public ErrorResponse handleNotFoundException(ChangeSetPersister.NotFoundException e, WebRequest request) {
        log.info("Entity not found: {}, Request details: {}", e, request);
        String userFriendlyMessage = "Resource not found";
        return new ErrorResponse(userFriendlyMessage, System.currentTimeMillis());
    }

    @ResponseStatus(HttpStatus.BAD_REQUEST)
    @ExceptionHandler(MethodArgumentNotValidException.class)
    public ErrorResponse handleValidationExceptions(MethodArgumentNotValidException e, WebRequest request) {
        String validationErrors = e.getBindingResult().getFieldErrors().stream()
                .map(fieldError -> fieldError.getField() + ": " + fieldError.getDefaultMessage())
                .collect(Collectors.joining(", "));
        log.warn("Validation failed: {}, Errors: {}, Request details: {}", e, validationErrors, request);
        return new ErrorResponse("Validation error: " + validationErrors, System.currentTimeMillis());
    }

    @ResponseStatus(HttpStatus.BAD_REQUEST)
    @ExceptionHandler(HttpMessageNotReadableException.class)
    public ErrorResponse handleHttpMessageNotReadableException(HttpMessageNotReadableException e, WebRequest request) {
        e.getMostSpecificCause();
        String detailedError = e.getMostSpecificCause().getMessage();

        log.error("Failed to read HTTP message. Detailed error: {}, Request details: {}, Message: {}", detailedError, request, e.getMessage());

        String userFriendlyMessage = "Invalid input. Please check your request format or parameters.";
        return new ErrorResponse(userFriendlyMessage, System.currentTimeMillis());
    }

    @ResponseStatus(HttpStatus.INTERNAL_SERVER_ERROR)
    @ExceptionHandler(Exception.class)
    public ErrorResponse handleGlobalException(Exception e, WebRequest request) {
        log.error("Unexpected error: {} {} {}", e.getMessage(), e, request);
        return new ErrorResponse("An unexpected error occurred", System.currentTimeMillis());
    }
}
