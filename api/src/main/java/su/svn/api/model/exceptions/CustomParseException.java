/*
 * This file was last modified at 2026.04.20 00:29 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * CustomParseException.java
 * $Id$
 */

package su.svn.api.model.exceptions;

import io.smallrye.jwt.auth.principal.ParseException;

public class CustomParseException extends RuntimeException {
    public CustomParseException(ParseException e) {
        super(e);
    }
}
