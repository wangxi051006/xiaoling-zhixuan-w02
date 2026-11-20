ALTER TABLE products ADD COLUMN image_url text NOT NULL DEFAULT ''
    CHECK (image_url = '' OR image_url ~ '^/api/v1/catalog/product-images/[a-f0-9]{32}-[a-f0-9]{64}\.(jpg|png)$');
