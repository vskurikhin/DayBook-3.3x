/*
 * This file was last modified at 2026.05.08 09:18 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordType.java
 * $Id$
 */

package su.svn.core.domain.entities;

/**
 * Enumeration of supported record types.
 *
 * <p>Defines the type of content stored in {@link BaseRecord}.</p>
 */
public enum RecordType {
    Array,
    Base,
    Blob,
    File,
    Folder,
    Json,
    Link,
    Markdown,
    Set,
    Text,
    Vector,
    Xml
}
