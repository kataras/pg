-- your commands here, can be splitted using ';'
CREATE OR REPLACE VIEW blog_master AS
SELECT b.*, COUNT(bp) as posts_count
  FROM blogs b
  INNER JOIN blog_posts bp ON blog_id = b.id
GROUP BY b.id;