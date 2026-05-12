/*
 * This file was last modified at 2026.05.08 09:18 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordType.java
 * $Id$
 */

package su.svn.lib;

/**
 * Enumeration of supported record types.
 *
 * <p>Defines the type of content stored in {@link su.svn.core.domain.entities.BaseRecord}.</p>
 */
public enum RecordType {
    Base,
    Blob,
    Json,
    Link,
    Markdown,
    Set,
    Text,
    Vector,
    Xml
}
