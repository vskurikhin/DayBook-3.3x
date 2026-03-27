/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ResourceRecordViewFilter.java
 * $Id$
 */

package su.svn.core.models.dto;

import java.time.OffsetDateTime;

public record ResourceRecordViewFilter(
        String title,
        OffsetDateTime fromDate,
        OffsetDateTime toDate) {
}
